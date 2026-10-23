// Package provider implements the RedirHub Terraform provider.
package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/redirhub/terraform-provider-redirhub/internal/client"
)

const defaultBaseURL = "https://api.redirhub.com/v1"

var _ provider.Provider = &redirhubProvider{}

type redirhubProvider struct {
	version string
}

type providerModel struct {
	APIToken  types.String `tfsdk:"api_token"`
	BaseURL   types.String `tfsdk:"base_url"`
	Workspace types.String `tfsdk:"workspace"`
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &redirhubProvider{version: version}
	}
}

func (p *redirhubProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "redirhub"
	resp.Version = p.version
}

func (p *redirhubProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage RedirHub domains and links (short links, QR codes, redirects and migration mappings) as code.",
		Attributes: map[string]schema.Attribute{
			"api_token": schema.StringAttribute{
				Description: "An API token from Account → API tokens. The token belongs to one workspace. Defaults to the REDIRHUB_API_TOKEN environment variable.",
				Optional:    true,
				Sensitive:   true,
			},
			"base_url": schema.StringAttribute{
				Description: "The API base URL. Defaults to REDIRHUB_API_URL, or " + defaultBaseURL + ".",
				Optional:    true,
			},
			"workspace": schema.StringAttribute{
				Description: "The workspace id, for a token that is not bound to a workspace. Defaults to REDIRHUB_WORKSPACE.",
				Optional:    true,
			},
		},
	}
}

func (p *redirhubProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	token := stringOr(config.APIToken, os.Getenv("REDIRHUB_API_TOKEN"))
	if token == "" {
		resp.Diagnostics.AddAttributeError(path.Root("api_token"), "Missing API token",
			"Set api_token in the provider block or the REDIRHUB_API_TOKEN environment variable.")
		return
	}
	baseURL := stringOr(config.BaseURL, os.Getenv("REDIRHUB_API_URL"))
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	workspace := stringOr(config.Workspace, os.Getenv("REDIRHUB_WORKSPACE"))

	c := client.New(baseURL, token, workspace, "Terraform terraform-provider-redirhub/"+p.version)
	resp.ResourceData = c
	resp.DataSourceData = c
}

func (p *redirhubProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewLinkResource,
		NewDomainResource,
		NewDomainVerificationResource,
	}
}

func (p *redirhubProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewDomainDataSource,
		NewLinksDataSource,
	}
}

func stringOr(v types.String, fallback string) string {
	if v.IsNull() || v.IsUnknown() || v.ValueString() == "" {
		return fallback
	}
	return v.ValueString()
}
