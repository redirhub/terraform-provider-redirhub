package provider

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/redirhub/terraform-provider-redirhub/internal/client"
)

var _ resource.ResourceWithConfigure = &domainVerificationResource{}

// verificationPollEvery is how often the DNS is checked again while waiting.
var verificationPollEvery = 15 * time.Second

func NewDomainVerificationResource() resource.Resource {
	return &domainVerificationResource{pollEvery: verificationPollEvery}
}

// domainVerificationResource waits until a domain points to RedirHub, the way
// aws_acm_certificate_validation waits for a certificate: it goes after the DNS records, and
// the links that need the domain go after it.
type domainVerificationResource struct {
	client    *client.Client
	pollEvery time.Duration
}

type domainVerificationModel struct {
	ID      types.String `tfsdk:"id"`
	Host    types.String `tfsdk:"host"`
	Timeout types.String `tfsdk:"timeout"`
}

func (r *domainVerificationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_domain_verification"
}

func (r *domainVerificationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Waits until a domain's DNS points to RedirHub. Create it after the DNS records and make the links that need the domain depend on it. It changes nothing in RedirHub, and destroying it only removes it from the state.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"host": schema.StringAttribute{
				Description:   "The domain to wait for, usually `redirhub_domain.<name>.host`.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"timeout": schema.StringAttribute{
				Description: "How long to wait, e.g. `10m` (the default) or `1h`. DNS changes can take a while to reach every resolver.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("10m"),
			},
		},
	}
}

func (r *domainVerificationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.client = req.ProviderData.(*client.Client)
}

func (r *domainVerificationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan domainVerificationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, err := time.ParseDuration(plan.Timeout.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid timeout", err.Error())
		return
	}

	host := plan.Host.ValueString()
	deadline := time.Now().Add(timeout)
	for {
		domain, err := r.client.RefreshDomain(ctx, host)
		if err != nil {
			resp.Diagnostics.AddError("Could not check the domain's DNS", err.Error())
			return
		}
		if domain.Correct {
			break
		}
		if time.Now().Add(r.pollEvery).After(deadline) {
			resp.Diagnostics.AddError("The domain does not point to RedirHub yet",
				fmt.Sprintf("%s did not point to RedirHub within %s. Check its records against redirhub_domain.dns_records, or raise timeout.", host, timeout))
			return
		}
		select {
		case <-time.After(r.pollEvery):
		case <-ctx.Done():
			resp.Diagnostics.AddError("Stopped waiting for the domain", ctx.Err().Error())
			return
		}
	}

	plan.ID = types.StringValue(host)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *domainVerificationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state domainVerificationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// A domain that pointed to RedirHub once stays verified here; only its removal undoes it.
	if _, err := r.client.GetDomain(ctx, state.Host.ValueString()); errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
	} else if err != nil {
		resp.Diagnostics.AddError("Could not read the domain", err.Error())
	}
}

func (r *domainVerificationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan domainVerificationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *domainVerificationResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}
