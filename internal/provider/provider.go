// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"golang.org/x/crypto/ssh"
)

// Ensure the implementation satisfies the expected interfaces.
var (
	_ provider.Provider = &saltyProvider{}
)

// New is a helper function to simplify provider server and testing implementation.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &saltyProvider{
			version: version,
		}
	}
}

type providerData struct {
	Username      string
	PrivateKey    string
	UyuniBaseURL  string
	UyuniUsername string
	UyuniPassword string
}

type saltyProviderModel struct {
	Username      types.String `tfsdk:"username"`
	PrivateKey    types.String `tfsdk:"private_key"`
	UyuniBaseURL  types.String `tfsdk:"uyuni_base_url"`
	UyuniUsername types.String `tfsdk:"uyuni_username"`
	UyuniPassword types.String `tfsdk:"uyuni_password"`
}

// saltyProvider is the provider implementation.
type saltyProvider struct {
	// version is set to the provider version on release, "dev" when the
	// provider is built and ran locally, and "test" when running acceptance
	// testing.
	version string
}

// Metadata returns the provider type name.
func (p *saltyProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "salty"
	resp.Version = p.version
}

// Schema defines the provider-level schema for configuration data.
func (p *saltyProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"username": schema.StringAttribute{
				Required: true,
			},
			"private_key": schema.StringAttribute{
				Sensitive: true,
				Required:  true,
			},
			"uyuni_base_url": schema.StringAttribute{
				Required: true,
			},
			"uyuni_username": schema.StringAttribute{
				Required: true,
			},
			"uyuni_password": schema.StringAttribute{
				Sensitive: true,
				Required:  true,
			},
		},
	}
}

// Configure prepares a Salty client for data sources and resources.
func (p *saltyProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	tflog.Info(ctx, "Configuring Salty provider")

	var config saltyProviderModel

	diags := req.Config.Get(ctx, &config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if config.Username.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("username"),
			"Unknown username for connecting to Salt Minion",
			"The provider cannot create the Salty client as there is an unknown configuration value for the Salty client username. ",
		)
	}

	if config.PrivateKey.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("private_key"),
			"Unknown private key for connecting to Salt Minion",
			"The provider cannot create the Salty client as there is an unknown configuration value for the Salty client private key. ",
		)
	}

	_, err := ssh.ParsePrivateKey([]byte(config.PrivateKey.ValueString()))
	if err != nil {
		resp.Diagnostics.AddAttributeError(
			path.Root("private_key"),
			"malformed private key for connecting to Salt Minion",
			"The provider cannot create the Salty client as there is a malformed configuration value for the Salty client private key. ",
		)
	}

	if config.UyuniBaseURL.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("uyuni_base_url"),
			"Unknown Uyuni base URL for connecting to Uyuni server",
			"The provider cannot create the Salty client as there is an unknown configuration value for the Salty Uyuni base URL. ",
		)
	}

	if config.UyuniUsername.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("uyuni_username"),
			"Unknown Uyuni username for connecting to Uyuni server",
			"The provider cannot create the Salty client as there is an unknown configuration value for the Salty Uyuni username. ",
		)
	}

	if config.UyuniPassword.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("uyuni_password"),
			"Unknown Uyuni password for connecting to Uyuni server",
			"The provider cannot create the Salty client as there is an unknown configuration value for the Salty Uyuni password. ",
		)
	}

	if resp.Diagnostics.HasError() {
		return
	}

	data := &providerData{
		Username:      config.Username.ValueString(),
		PrivateKey:    config.PrivateKey.ValueString(),
		UyuniBaseURL:  config.UyuniBaseURL.ValueString(),
		UyuniUsername: config.UyuniUsername.ValueString(),
		UyuniPassword: config.UyuniPassword.ValueString(),
	}
	resp.ResourceData = data
	resp.DataSourceData = data
}

// DataSources defines the data sources implemented in the provider.
func (p *saltyProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return nil
}

// Resources defines the resources implemented in the provider.
func (p *saltyProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewGrainResource,
		NewGrainStringResource,
	}
}
