package provider

import (
	"context"
	"errors"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/redirhub/terraform-provider-redirhub/internal/client"
)

var (
	_ resource.ResourceWithConfigure   = &domainResource{}
	_ resource.ResourceWithImportState = &domainResource{}
)

func NewDomainResource() resource.Resource { return &domainResource{} }

type domainResource struct {
	client *client.Client
}

type domainModel struct {
	ID         types.String `tfsdk:"id"`
	Host       types.String `tfsdk:"host"`
	HTTPS      types.Bool   `tfsdk:"https"`
	ForceHTTPS types.Bool   `tfsdk:"force_https"`
	ShortLinks types.Bool   `tfsdk:"short_links"`
	Mode       types.String `tfsdk:"connection_mode"`
	DNSOK      types.Bool   `tfsdk:"dns_ok"`
	DNSRecords types.List   `tfsdk:"dns_records"`
}

// dnsRecordType is one record the domain needs, ready for a DNS provider's record resource.
var dnsRecordType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"name":  types.StringType,
	"type":  types.StringType,
	"value": types.StringType,
}}

func (r *domainResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_domain"
}

func (r *domainResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A domain connected to the workspace. Its `dns_records` can feed your DNS provider's record resource, so the domain is connected in the same apply.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "The hostname.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"host": schema.StringAttribute{
				Required:      true,
				Description:   "The hostname, e.g. `go.example.com`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"https": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				Description: "Issue and renew a certificate for the domain.",
			},
			"force_https": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				Description: "Send HTTP visitors to HTTPS (Enterprise).",
			},
			"short_links": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				Description: "Allow short links and QR codes on the domain.",
			},
			"connection_mode": schema.StringAttribute{
				Optional: true, Computed: true, Default: stringdefault.StaticString("managed"),
				Description: "How the domain is served. `managed` (the default): its DNS points to RedirHub, which serves its redirects, links and HTTPS. " +
					"`self_hosted`: your own servers run the RedirHub container and serve HTTPS for it with the certificates RedirHub issues and renews; " +
					"point its DNS at your servers. Needs a workspace with Self-Hosted TLS. Changing it switches the domain in place.",
				Validators: []validator.String{stringvalidator.OneOf("managed", "self_hosted")},
			},
			"dns_ok": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the domain was connected at the last check: its DNS points to RedirHub, or for `self_hosted` your servers answered for it.",
			},
			"dns_records": schema.ListAttribute{
				Computed:      true,
				ElementType:   dnsRecordType,
				Description:   "The records to create at your DNS provider. `managed`: the CNAME (or A record for an apex domain) and the TXT verification record. `self_hosted`: none for a hostname, whose DNS points at your servers; for a wildcard, the `_acme-challenge` CNAME that proves it.",
				PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown(), recordsFollowMode{}},
			},
		},
	}
}

// recordsFollowMode leaves dns_records unknown when connection_mode changes, since the records
// the domain needs change with it.
type recordsFollowMode struct{}

func (recordsFollowMode) Description(context.Context) string {
	return "The records change when connection_mode does."
}

func (m recordsFollowMode) MarkdownDescription(ctx context.Context) string { return m.Description(ctx) }

func (recordsFollowMode) PlanModifyList(ctx context.Context, req planmodifier.ListRequest, resp *planmodifier.ListResponse) {
	if req.State.Raw.IsNull() {
		return
	}
	var planned, current types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("connection_mode"), &planned)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("connection_mode"), &current)...)
	if !planned.Equal(current) {
		resp.PlanValue = types.ListUnknown(dnsRecordType)
	}
}

func (r *domainResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.client = req.ProviderData.(*client.Client)
}

func (r *domainResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan domainModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	domain, err := r.client.CreateDomain(ctx, map[string]any{
		"host":                plan.Host.ValueString(),
		"https_requested":     plan.HTTPS.ValueBool(),
		"short_links_enabled": plan.ShortLinks.ValueBool(),
		"connection_mode":     plan.Mode.ValueString(),
	})
	if err == nil && plan.ForceHTTPS.ValueBool() {
		domain, err = r.client.UpdateDomain(ctx, domain.Host, map[string]any{"force_https": true})
	}
	if err != nil {
		resp.Diagnostics.AddError("Could not connect the domain", err.Error())
		return
	}
	r.fill(&plan, domain)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *domainResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state domainModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	domain, err := r.client.GetDomain(ctx, state.ID.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Could not read the domain", err.Error())
		return
	}
	r.fill(&state, domain)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *domainResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state domainModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	changes := map[string]any{
		"https_requested":     plan.HTTPS.ValueBool(),
		"force_https":         plan.ForceHTTPS.ValueBool(),
		"short_links_enabled": plan.ShortLinks.ValueBool(),
	}
	// Sent only when it changes: switching is the one thing the API checks it for.
	if !plan.Mode.Equal(state.Mode) {
		changes["connection_mode"] = plan.Mode.ValueString()
	}
	domain, err := r.client.UpdateDomain(ctx, state.ID.ValueString(), changes)
	if err != nil {
		resp.Diagnostics.AddError("Could not update the domain", err.Error())
		return
	}
	r.fill(&plan, domain)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *domainResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state domainModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteDomain(ctx, state.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Could not remove the domain", err.Error())
	}
}

// ImportState takes the hostname.
func (r *domainResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("host"), req.ID)...)
}

func (r *domainResource) fill(m *domainModel, d *client.Domain) {
	m.ID = types.StringValue(d.Host)
	if m.Host.IsNull() || m.Host.IsUnknown() || !strings.EqualFold(m.Host.ValueString(), d.Host) {
		m.Host = types.StringValue(d.Host)
	}
	m.HTTPS = types.BoolValue(d.HTTPSRequested)
	m.ForceHTTPS = types.BoolValue(d.ForceHTTPS)
	m.ShortLinks = types.BoolValue(d.ShortLinksEnabled)
	m.Mode = types.StringValue(d.ConnectionMode)
	m.DNSOK = types.BoolValue(connected(d))
	m.DNSRecords = dnsRecords(d)
}

// connected reports whether the domain was connected at the last check: its DNS points to
// RedirHub, or a domain proven by a check (self-hosted) passed it.
func connected(d *client.Domain) bool {
	if d.ConnectionMode == "" || d.ConnectionMode == "managed" {
		return d.Correct
	}
	return d.Status == "connected"
}

// dnsRecords lists the records the domain needs: the pointing record RedirHub suggests for it
// (a CNAME, or an A record where a CNAME cannot go) and the verification TXT record. A
// self-hosted domain points at your own servers instead, so it needs none of them; a wildcard
// still needs its `_acme-challenge` CNAME.
func dnsRecords(d *client.Domain) types.List {
	elements := []attr.Value{}
	if d.ConnectionMode != "" && d.ConnectionMode != "managed" {
		if d.SelfHosted != nil && d.SelfHosted.Record != nil {
			elements = append(elements, dnsRecordValue(*d.SelfHosted.Record))
		}
		return types.ListValueMust(dnsRecordType, elements)
	}
	for _, rec := range d.Require {
		wanted := rec.Scope == d.DefaultRequire || strings.HasPrefix(rec.Scope, "ver")
		if !wanted || rec.Value == "" {
			continue
		}
		elements = append(elements, dnsRecordValue(rec))
	}
	return types.ListValueMust(dnsRecordType, elements)
}

func dnsRecordValue(rec client.DNSRecord) attr.Value {
	return types.ObjectValueMust(dnsRecordType.AttrTypes, map[string]attr.Value{
		"name":  types.StringValue(rec.Name),
		"type":  types.StringValue(rec.Type),
		"value": types.StringValue(rec.Value),
	})
}
