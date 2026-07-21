// Package provider implements the "openinfra" Terraform provider.
package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/harn3ss/terraform-provider-openinfra/internal/client"
)

// Ensure the implementation satisfies the framework interfaces.
var _ provider.Provider = (*openinfraProvider)(nil)

type openinfraProvider struct {
	version string
}

// New returns a provider factory for the given build version.
func New(version string) func() provider.Provider {
	return func() provider.Provider { return &openinfraProvider{version: version} }
}

func (p *openinfraProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "openinfra"
	resp.Version = p.version
}

type providerModel struct {
	Kubeconfig types.String `tfsdk:"kubeconfig"`
	Context    types.String `tfsdk:"context"`
}

func (p *openinfraProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage [open-infra](https://github.com/harn3ss/open-infra) resources " +
			"(applications, databases, virtual machines, …) declared as Kubernetes custom resources.",
		Attributes: map[string]schema.Attribute{
			"kubeconfig": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Path to a kubeconfig file. Defaults to in-cluster credentials " +
					"when running inside the cluster, otherwise `$KUBECONFIG` or `~/.kube/config`.",
			},
			"context": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Kubeconfig context to use. Defaults to the file's current context.",
			},
		},
	}
}

func (p *openinfraProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	c, err := client.New(client.Config{
		Kubeconfig: cfg.Kubeconfig.ValueString(),
		Context:    cfg.Context.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to reach the open-infra cluster",
			"Could not build a Kubernetes client. Check the provider's kubeconfig/context, "+
				"or that this machine can reach the API server.\n\n"+err.Error(),
		)
		return
	}

	// Both resources and data sources receive the client.
	resp.ResourceData = c
	resp.DataSourceData = c
}

func (p *openinfraProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewApplicationResource,
		NewDatabaseResource,
		NewVirtualMachineResource,
	}
}

func (p *openinfraProvider) DataSources(context.Context) []func() datasource.DataSource {
	return nil
}

func (p *openinfraProvider) Functions(context.Context) []func() function.Function {
	return nil
}
