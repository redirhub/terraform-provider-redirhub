package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/redirhub/terraform-provider-redirhub/internal/client"
)

var _ datasource.DataSourceWithConfigure = &domainDataSource{}

func NewDomainDataSource() datasource.DataSource { return &domainDataSource{} }

// domainDataSource reads a domain connected elsewhere, e.g. in the dashboard.
type domainDataSource struct {
	client *client.Client
}

func (d *domainDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_domain"
}

func (d *domainDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A domain already connected to the workspace.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true},
			"host":        schema.StringAttribute{Required: true},
			"https":       schema.BoolAttribute{Computed: true},
			"force_https": schema.BoolAttribute{Computed: true},
			"short_links": schema.BoolAttribute{Computed: true},
			"dns_ok":      schema.BoolAttribute{Computed: true},
			"dns_records": schema.ListAttribute{Computed: true, ElementType: dnsRecordType},
		},
	}
}

func (d *domainDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	d.client = req.ProviderData.(*client.Client)
}

func (d *domainDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m domainModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	domain, err := d.client.GetDomain(ctx, m.Host.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Could not read the domain", err.Error())
		return
	}
	(&domainResource{}).fill(&m, domain)
	m.Host = types.StringValue(domain.Host)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
