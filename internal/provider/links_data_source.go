package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/redirhub/terraform-provider-redirhub/internal/client"
)

var _ datasource.DataSourceWithConfigure = &linksDataSource{}

func NewLinksDataSource() datasource.DataSource { return &linksDataSource{} }

// linksDataSource lists the workspace's links with the filters of `GET /v1/links`.
type linksDataSource struct {
	client *client.Client
}

type linksModel struct {
	Handler types.String `tfsdk:"handler"`
	Host    types.String `tfsdk:"host"`
	Path    types.String `tfsdk:"path"`
	Status  types.String `tfsdk:"status"`
	Tags    types.Set    `tfsdk:"tags"`
	Search  types.String `tfsdk:"search"`
	Links   types.List   `tfsdk:"links"`
}

var listedLinkType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"id":           types.StringType,
	"handler":      types.StringType,
	"url":          types.StringType,
	"host":         types.StringType,
	"alias":        types.StringType,
	"destinations": types.ListType{ElemType: types.StringType},
	"type":         types.StringType,
	"status":       types.StringType,
	"tags":         types.SetType{ElemType: types.StringType},
}}

func (d *linksDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_links"
}

func (d *linksDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	filter := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{Description: desc, Optional: true}
	}
	resp.Schema = schema.Schema{
		Description: "The workspace's links, newest first, narrowed by the filters of `GET /v1/links`. Useful to find links made in the dashboard before importing them.",
		Attributes: map[string]schema.Attribute{
			"handler": filter("Only these handlers, comma-separated: `short-url`, `qr`, `redirect`, `migration`."),
			"host":    filter("Only links on this hostname."),
			"path":    filter("Only links on this path: `/` for a hostname's root, `*` for the link that answers every other path."),
			"status": schema.StringAttribute{
				Description: "Only `active` or `paused` links; `paused` includes links paused by the plan or its visit limit.",
				Optional:    true,
				Validators:  []validator.String{stringvalidator.OneOf("active", "paused")},
			},
			"tags":   schema.SetAttribute{Description: "Only links with any of these tags.", Optional: true, ElementType: types.StringType},
			"search": filter("Only links whose URL, destination or title contains this text."),
			"links": schema.ListAttribute{
				Description: "The matching links. `status` is as the API reports it, e.g. `active`, `paused` or `dns_incorrect`.",
				Computed:    true,
				ElementType: listedLinkType,
			},
		},
	}
}

func (d *linksDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	d.client = req.ProviderData.(*client.Client)
}

func (d *linksDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m linksModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}

	filter := map[string]string{}
	for name, v := range map[string]types.String{"handler": m.Handler, "host": m.Host, "path": m.Path, "status": m.Status, "search": m.Search} {
		if known(v) {
			filter[name] = v.ValueString()
		}
	}
	if known(m.Tags) {
		var tags []string
		resp.Diagnostics.Append(m.Tags.ElementsAs(ctx, &tags, false)...)
		filter["tags"] = strings.Join(tags, ",")
	}

	links, err := d.client.ListLinks(ctx, filter)
	if err != nil {
		resp.Diagnostics.AddError("Could not list the links", err.Error())
		return
	}

	elements := make([]attr.Value, 0, len(links))
	for _, l := range links {
		destinations, diags := types.ListValueFrom(ctx, types.StringType, l.Destinations)
		resp.Diagnostics.Append(diags...)
		tags, diags := types.SetValueFrom(ctx, types.StringType, l.Tags)
		resp.Diagnostics.Append(diags...)
		elements = append(elements, types.ObjectValueMust(listedLinkType.AttrTypes, map[string]attr.Value{
			"id":           types.StringValue(l.ID),
			"handler":      types.StringValue(l.Handler),
			"url":          types.StringValue(l.URL),
			"host":         types.StringValue(l.Host),
			"alias":        optionalString(l.Alias),
			"destinations": destinations,
			"type":         optionalString(l.Type),
			"status":       types.StringValue(l.Status),
			"tags":         tags,
		}))
	}
	m.Links = types.ListValueMust(listedLinkType, elements)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
