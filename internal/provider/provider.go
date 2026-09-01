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
	// Application, Database and VirtualMachine are hand-written: they carry behaviour a
	// schema table cannot express (per-engine connection-secret naming, start/stop
	// semantics). Everything else is generated from the kindSpec table in kinds.go.
	out := []func() resource.Resource{
		NewApplicationResource,
		NewDatabaseResource,
		NewVirtualMachineResource,
	}
	for _, k := range genericKinds {
		out = append(out, newGenericResource(k))
	}
	return out
}

// crdKinds is every open-infra CRD: terraform type suffix, plural, Kind. Data
// sources are generated for all of them so the whole platform is readable even
// where a typed resource doesn't exist yet.
//
// KEEP IN SYNC with the CRDs in open-infra (platform/abstraction/*-xrd.yaml).
// Adding a kind there and forgetting it here means it simply isn't addressable
// from Terraform — there's no error, it's just missing.
var crdKinds = []struct{ typeName, plural, kind string }{
	{"application", "applications", "Application"},
	{"batch_transform", "batchtransforms", "BatchTransform"},
	{"certificate_authority", "certificateauthorities", "CertificateAuthority"},
	{"database_proxy", "databaseproxies", "DatabaseProxy"},
	{"dataflow", "dataflows", "DataFlow"},
	{"directory", "directories", "Directory"},
	{"user_pool", "userpools", "UserPool"},
	{"fault_injection", "faultinjections", "FaultInjection"},
	{"feature_group", "featuregroups", "FeatureGroup"},
	{"file_share", "fileshares", "FileShare"},
	{"function", "functions", "Function"},
	{"graphql_api", "graphqlapis", "GraphQLApi"},
	{"http_api", "httpapis", "HttpApi"},
	{"migration", "migrations", "Migration"},
	{"model", "models", "Model"},
	{"model_monitor", "modelmonitors", "ModelMonitor"},
	{"model_package", "modelpackages", "ModelPackage"},
	{"processing_job", "processingjobs", "ProcessingJob"},
	{"query", "queries", "Query"},
	{"replication", "replications", "Replication"},
	{"security_group", "securitygroups", "SecurityGroup"},
	{"state_machine", "statemachines", "StateMachine"},
	{"stream", "streams", "Stream"},
	{"training_job", "trainingjobs", "TrainingJob"},
	{"virtual_machine", "virtualmachines", "VirtualMachine"},
	{"vm_image", "vmimages", "VmImage"},
	{"volume", "volumes", "Volume"},
}

func (p *openinfraProvider) DataSources(context.Context) []func() datasource.DataSource {
	out := make([]func() datasource.DataSource, 0, len(crdKinds))
	for _, k := range crdKinds {
		out = append(out, newDataSource(k.typeName, k.plural, k.kind))
	}
	return out
}

func (p *openinfraProvider) Functions(context.Context) []func() function.Function {
	return nil
}
