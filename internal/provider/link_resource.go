package provider

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/redirhub/terraform-provider-redirhub/internal/client"
)

// The four intents a link is created from, as the API's `handler` names them. They are managed
// as one kind of object, so one resource covers them all.
const (
	handlerShortURL  = "short-url"
	handlerQR        = "qr"
	handlerRedirect  = "redirect"
	handlerMigration = "migration"
)

func isShortLink(handler string) bool { return handler == handlerShortURL || handler == handlerQR }

var (
	_ resource.ResourceWithConfigure      = &linkResource{}
	_ resource.ResourceWithImportState    = &linkResource{}
	_ resource.ResourceWithValidateConfig = &linkResource{}
)

func NewLinkResource() resource.Resource { return &linkResource{} }

type linkResource struct {
	client *client.Client
}

type utmModel struct {
	Source   types.String `tfsdk:"source"`
	Medium   types.String `tfsdk:"medium"`
	Campaign types.String `tfsdk:"campaign"`
	Term     types.String `tfsdk:"term"`
	Content  types.String `tfsdk:"content"`
}

var qrAttrTypes = map[string]attr.Type{
	"theme":    types.StringType,
	"color":    types.StringType,
	"logo":     types.BoolType,
	"show_url": types.BoolType,
	"margin":   types.StringType,
}

// linkModel mirrors a record of `/v1/links`, field for field.
type linkModel struct {
	ID                 types.String `tfsdk:"id"`
	Handler            types.String `tfsdk:"handler"`
	URL                types.String `tfsdk:"url"`
	Host               types.String `tfsdk:"host"`
	Alias              types.String `tfsdk:"alias"`
	HTTPS              types.Bool   `tfsdk:"https"`
	Destinations       types.List   `tfsdk:"destinations"`
	DestinationRouting types.String `tfsdk:"destination_routing"`
	Type               types.String `tfsdk:"type"`
	Title              types.String `tfsdk:"title"`
	Description        types.String `tfsdk:"description"`
	ForwardPath        types.Bool   `tfsdk:"forward_path"`
	ForwardQuery       types.Bool   `tfsdk:"forward_query"`
	Status             types.String `tfsdk:"status"`
	Tags               types.Set    `tfsdk:"tags"`
	UTM                *utmModel    `tfsdk:"utm"`
	QR                 types.Object `tfsdk:"qr"`
	Password           types.String `tfsdk:"password"`
}

func (r *linkResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_link"
}

func (r *linkResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keepString := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	keepBool := []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()}
	optionalComputedString := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{Description: desc, Optional: true, Computed: true, PlanModifiers: keepString}
	}
	optionalComputedBool := func(desc string) schema.BoolAttribute {
		return schema.BoolAttribute{Description: desc, Optional: true, Computed: true, PlanModifiers: keepBool}
	}
	utmString := func() schema.StringAttribute { return schema.StringAttribute{Optional: true} }

	resp.Schema = schema.Schema{
		Description: "A link of the workspace: a short link (`short-url`), a dynamic QR code (`qr`), a domain redirect (`redirect`) or a website migration mapping (`migration`). " +
			"The attributes are the fields of `/v1/links`. Short links take `host` and `alias`; redirects and migration mappings take `url`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description:   "The link id, e.g. `link_7bXmR4`.",
				Computed:      true,
				PlanModifiers: keepString,
			},
			"handler": schema.StringAttribute{
				Description:   "What the link is: `short-url`, `qr`, `redirect` or `migration`. Changing it replaces the link.",
				Required:      true,
				Validators:    []validator.String{stringvalidator.OneOf(handlerShortURL, handlerQR, handlerRedirect, handlerMigration)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"url": schema.StringAttribute{
				Description: "The URL the link answers on. Set it for a redirect or a migration mapping (`old.example.com`, `example.com/blog/*`; the scheme and a trailing slash are ignored); " +
					"a short link's is `host/alias`. Changing it replaces the link.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIfConfigured(), stringplanmodifier.UseStateForUnknown()},
			},
			"host": schema.StringAttribute{
				Description:   "The hostname. Set it for a short link or QR code (a short-link domain of the workspace); a redirect's comes from `url`. Changing it replaces the link.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIfConfigured(), stringplanmodifier.UseStateForUnknown()},
			},
			"alias": schema.StringAttribute{
				Description:   "A short link's path after the host. Left out, RedirHub picks one. Changing it replaces the link.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIfConfigured(), stringplanmodifier.UseStateForUnknown()},
			},
			"https": schema.BoolAttribute{
				Description:   "Whether the link is served over HTTPS.",
				Computed:      true,
				PlanModifiers: keepBool,
			},
			"destinations": schema.ListAttribute{
				Description: "Where visitors are sent, the primary destination first. More than one is shared out by `destination_routing` (plan feature).",
				ElementType: types.StringType,
				Required:    true,
				Validators:  []validator.List{listvalidator.SizeAtLeast(1)},
			},
			"destination_routing": optionalComputedString("How several destinations share the traffic, e.g. `random`, `weighted`, `fastest`. `GET /v1/shared/types` lists them."),
			"type":                optionalComputedString("The redirect type, e.g. `301`, `302`, `307`, `308`, `frame`. `GET /v1/shared/types` lists them."),
			"title":               schema.StringAttribute{Description: "A title shown in the dashboard.", Optional: true},
			"description":         schema.StringAttribute{Description: "A note shown in the dashboard.", Optional: true},
			"forward_path":        optionalComputedBool("Append the requested path to the destination."),
			"forward_query":       optionalComputedBool("Append the requested query string to the destination."),
			"status": schema.StringAttribute{
				Description: "`active`, or `paused` to stop serving the link.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("active"),
				Validators:  []validator.String{stringvalidator.OneOf("active", "paused")},
			},
			"tags": schema.SetAttribute{
				Description: "Tags (plan feature).",
				ElementType: types.StringType,
				Optional:    true,
			},
			"utm": schema.SingleNestedAttribute{
				Description: "UTM parameters added to the destination.",
				Optional:    true,
				Attributes: map[string]schema.Attribute{
					"source": utmString(), "medium": utmString(), "campaign": utmString(), "term": utmString(), "content": utmString(),
				},
			},
			"qr": schema.SingleNestedAttribute{
				Description:   "The QR code style of a short link. A `qr` link always has one; a `short-url` link has one once it is set.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
				Attributes: map[string]schema.Attribute{
					"theme":    optionalComputedString("`light` or `dark`."),
					"color":    optionalComputedString("The code's colour, e.g. `#181D27`."),
					"logo":     optionalComputedBool("Draw the workspace logo in the middle."),
					"show_url": optionalComputedBool("Print the link under the code."),
					"margin":   optionalComputedString("The quiet zone: `s`, `m` or `l`."),
				},
			},
			"password": schema.StringAttribute{
				Description: "Ask visitors for this password. The API never returns it, so a change made outside Terraform is not detected.",
				Optional:    true,
				Sensitive:   true,
			},
		},
	}
}

// ValidateConfig checks at plan time which fields each handler takes, as `POST /v1/links` does.
func (r *linkResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m linkModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || m.Handler.IsUnknown() || m.Handler.IsNull() {
		return
	}
	handler := m.Handler.ValueString()
	configured := func(v attr.Value) bool { return !v.IsNull() }

	if isShortLink(handler) {
		if m.Host.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("host"), "Missing host", "A "+handler+" link needs the short-link domain it is served on in host.")
		}
		if configured(m.URL) {
			resp.Diagnostics.AddAttributeError(path.Root("url"), "url is not set on a short link", "A short link's url is host/alias; set host and alias instead.")
		}
		return
	}

	if m.URL.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("url"), "Missing url", "A "+handler+" link needs the URL it answers for in url.")
	}
	for name, v := range map[string]attr.Value{"host": m.Host, "alias": m.Alias, "qr": m.QR} {
		if configured(v) {
			resp.Diagnostics.AddAttributeError(path.Root(name), name+" is not set on a "+handler, "A "+handler+" link answers on its url; "+name+" only applies to short links.")
		}
	}
}

func (r *linkResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.client = req.ProviderData.(*client.Client)
}

func (r *linkResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan linkModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	input := r.input(ctx, plan, &resp.Diagnostics)
	input["handler"] = plan.Handler.ValueString()
	if isShortLink(plan.Handler.ValueString()) {
		input["host"] = plan.Host.ValueString()
		if known(plan.Alias) {
			input["alias"] = plan.Alias.ValueString()
		}
	} else {
		input["url"] = plan.URL.ValueString()
	}
	if resp.Diagnostics.HasError() {
		return
	}

	link, err := r.client.CreateLink(ctx, input)
	if err != nil {
		resp.Diagnostics.AddError("Could not create the link", err.Error())
		return
	}
	r.fill(ctx, &plan, link, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *linkResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state linkModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	link, err := r.client.GetLink(ctx, state.ID.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Could not read the link", err.Error())
		return
	}
	r.fill(ctx, &state, link, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *linkResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state linkModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	input := r.input(ctx, plan, &resp.Diagnostics)
	// The API keeps a field it is not sent, so a field removed from the config is cleared explicitly.
	if plan.Title.IsNull() && !state.Title.IsNull() {
		input["title"] = nil
	}
	if plan.Description.IsNull() && !state.Description.IsNull() {
		input["description"] = nil
	}
	if plan.Password.IsNull() && !state.Password.IsNull() {
		input["password"] = nil
	}
	if plan.Tags.IsNull() && !state.Tags.IsNull() {
		input["tags"] = []string{}
	}
	// The API takes no null UTM; an empty set of parameters removes them.
	if plan.UTM == nil && state.UTM != nil {
		input["utm"] = map[string]string{}
	}
	if resp.Diagnostics.HasError() {
		return
	}

	link, err := r.client.UpdateLink(ctx, state.ID.ValueString(), input)
	if err != nil {
		resp.Diagnostics.AddError("Could not update the link", err.Error())
		return
	}
	plan.ID = state.ID
	r.fill(ctx, &plan, link, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *linkResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state linkModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteLink(ctx, state.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Could not delete the link", err.Error())
	}
}

// ImportState takes the link id shown in the dashboard and returned by the API, of any handler.
func (r *linkResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// input is the request body for the fields both create and update send.
func (r *linkResource) input(ctx context.Context, m linkModel, diags *diag.Diagnostics) map[string]any {
	input := map[string]any{}

	var destinations []string
	diags.Append(m.Destinations.ElementsAs(ctx, &destinations, false)...)
	input["destinations"] = destinations

	setString := func(field string, v types.String) {
		if known(v) {
			input[field] = v.ValueString()
		}
	}
	setBool := func(field string, v types.Bool) {
		if known(v) {
			input[field] = v.ValueBool()
		}
	}
	setString("destination_routing", m.DestinationRouting)
	setString("type", m.Type)
	setString("title", m.Title)
	setString("description", m.Description)
	setString("password", m.Password)
	setBool("forward_path", m.ForwardPath)
	setBool("forward_query", m.ForwardQuery)

	// The API takes the status as a number: 1 active, 0 paused.
	if m.Status.ValueString() == "paused" {
		input["status"] = 0
	} else {
		input["status"] = 1
	}

	if known(m.Tags) {
		var tags []string
		diags.Append(m.Tags.ElementsAs(ctx, &tags, false)...)
		input["tags"] = tags
	}

	if m.UTM != nil {
		input["utm"] = client.UTM{
			Source:   m.UTM.Source.ValueString(),
			Medium:   m.UTM.Medium.ValueString(),
			Campaign: m.UTM.Campaign.ValueString(),
			Term:     m.UTM.Term.ValueString(),
			Content:  m.UTM.Content.ValueString(),
		}
	}

	// Only the style fields the config sets are sent; the API keeps the others.
	if known(m.QR) && isShortLink(m.Handler.ValueString()) {
		style := map[string]any{}
		for name, v := range m.QR.Attributes() {
			switch v := v.(type) {
			case types.String:
				if known(v) {
					style[name] = v.ValueString()
				}
			case types.Bool:
				if known(v) {
					style[name] = v.ValueBool()
				}
			}
		}
		input["qr"] = style
	}
	return input
}

// fill copies the API's link into the model, keeping the config's spelling where the API only
// normalised it, so a plan shows no change that is not real.
func (r *linkResource) fill(ctx context.Context, m *linkModel, l *client.Link, diags *diag.Diagnostics) {
	m.ID = types.StringValue(l.ID)
	m.Handler = types.StringValue(l.Handler)
	m.HTTPS = types.BoolValue(l.HTTPS)

	if !known(m.URL) || normalizeURL(m.URL.ValueString()) != normalizeURL(l.URL) {
		m.URL = types.StringValue(l.URL)
	}
	if !known(m.Host) || !strings.EqualFold(m.Host.ValueString(), l.Host) {
		m.Host = types.StringValue(l.Host)
	}
	m.Alias = optionalString(l.Alias)

	destinations, d := types.ListValueFrom(ctx, types.StringType, l.Destinations)
	diags.Append(d...)
	m.Destinations = destinations

	m.DestinationRouting = optionalString(l.DestinationRouting)
	m.Type = optionalString(l.Type)
	m.Title = optionalString(l.Title)
	m.Description = optionalString(l.Description)
	m.ForwardPath = optionalBool(l.ForwardPath)
	m.ForwardQuery = optionalBool(l.ForwardQuery)
	if l.Paused() {
		m.Status = types.StringValue("paused")
	} else {
		m.Status = types.StringValue("active")
	}

	// No tags and none configured stays null rather than an empty set.
	if len(l.Tags) == 0 && m.Tags.IsNull() {
		m.Tags = types.SetNull(types.StringType)
	} else {
		tags, d := types.SetValueFrom(ctx, types.StringType, l.Tags)
		diags.Append(d...)
		m.Tags = tags
	}

	if l.UTM == nil || *l.UTM == (client.UTM{}) {
		m.UTM = nil
	} else {
		m.UTM = &utmModel{
			Source:   nonEmpty(l.UTM.Source),
			Medium:   nonEmpty(l.UTM.Medium),
			Campaign: nonEmpty(l.UTM.Campaign),
			Term:     nonEmpty(l.UTM.Term),
			Content:  nonEmpty(l.UTM.Content),
		}
	}

	if l.QR == nil {
		m.QR = types.ObjectNull(qrAttrTypes)
	} else {
		m.QR = types.ObjectValueMust(qrAttrTypes, map[string]attr.Value{
			"theme":    nonEmpty(l.QR.Theme),
			"color":    nonEmpty(l.QR.Color),
			"logo":     optionalBool(l.QR.Logo),
			"show_url": optionalBool(l.QR.ShowURL),
			"margin":   nonEmpty(l.QR.Margin),
		})
	}

	// The password is write-only: keep what the config holds, and forget it once the API says none is set.
	if !l.HasPassword {
		m.Password = types.StringNull()
	}
}

// normalizeURL compares link URLs the way the API stores them: no scheme, a lower-case
// hostname and no trailing slash.
func normalizeURL(s string) string {
	s = strings.TrimSpace(s)
	if u, err := url.Parse(s); err == nil && u.Scheme != "" && u.Host != "" {
		s = u.Host + u.RequestURI()
	}
	host, rest, _ := strings.Cut(s, "/")
	s = strings.ToLower(host)
	if rest != "" {
		s += "/" + rest
	}
	return strings.TrimRight(s, "/")
}

func known(v attr.Value) bool { return !v.IsNull() && !v.IsUnknown() }

func optionalString(v *string) types.String {
	if v == nil {
		return types.StringNull()
	}
	return types.StringValue(*v)
}

func optionalBool(v *bool) types.Bool {
	if v == nil {
		return types.BoolNull()
	}
	return types.BoolValue(*v)
}

func nonEmpty(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}
