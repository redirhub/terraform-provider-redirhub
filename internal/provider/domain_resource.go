package provider

import (
	"context"
	"errors"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
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
		Description: "A domain connected to the workspace. Its `dns_records` can feed your DNS provider's record resource, so the domain points to RedirHub in the same apply.",
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
			"dns_ok": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the domain's DNS pointed to RedirHub at the last check.",
			},
			"dns_records": schema.ListAttribute{
				Computed:      true,
				ElementType:   dnsRecordType,
				Description:   "The records to create at your DNS provider: the CNAME (or A record for an apex domain) and the TXT verification record.",
				PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
			},
		},
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
	domain, err := r.client.UpdateDomain(ctx, state.ID.ValueString(), map[string]any{
		"https_requested":     plan.HTTPS.ValueBool(),
		"force_https":         plan.ForceHTTPS.ValueBool(),
		"short_links_enabled": plan.ShortLinks.ValueBool(),
	})
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
	m.DNSOK = types.BoolValue(d.Correct)
	m.DNSRecords = dnsRecords(d)
}

// dnsRecords lists the records the domain needs: the pointing record RedirHub suggests for it
// (a CNAME, or an A record where a CNAME cannot go) and the verification TXT record.
func dnsRecords(d *client.Domain) types.List {
	elements := []attr.Value{}
	for _, rec := range d.Require {
		wanted := rec.Scope == d.DefaultRequire || strings.HasPrefix(rec.Scope, "ver")
		if !wanted || rec.Value == "" {
			continue
		}
		elements = append(elements, types.ObjectValueMust(dnsRecordType.AttrTypes, map[string]attr.Value{
			"name":  types.StringValue(rec.Name),
			"type":  types.StringValue(rec.Type),
			"value": types.StringValue(rec.Value),
		}))
	}
	return types.ListValueMust(dnsRecordType, elements)
}
