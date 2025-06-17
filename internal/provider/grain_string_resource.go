// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"golang.org/x/crypto/ssh"
	"time"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &GrainStringResource{}
var _ resource.ResourceWithImportState = &GrainStringResource{}

func NewGrainStringResource() resource.Resource {
	return &GrainStringResource{}
}

// GrainResource defines the resource implementation.
type GrainStringResource struct {
	username      *string
	privateKey    *string
	uyuniBaseURL  *string
	uyuniUsername *string
	uyuniPassword *string
}

// GrainResourceModel describes the resource data model.
type GrainStringResourceModel struct {
	Id         types.String `tfsdk:"id"`
	Server     types.String `tfsdk:"server"`
	GrainKey   types.String `tfsdk:"grain_key"`
	GrainValue types.String `tfsdk:"grain_value"`
	ApplyState types.Bool   `tfsdk:"apply_state"`
}

type SaltGrainStringModel struct {
	Value string `json:"local"`
}

func (r *GrainStringResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_grain_string"
}

func (r *GrainStringResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		// This description is used by the documentation generator and the language server.
		MarkdownDescription: "Salt Grain resource (string)",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
			},
			"server": schema.StringAttribute{
				Required: true,
			},
			"grain_key": schema.StringAttribute{
				Required: true,
			},
			"grain_value": schema.StringAttribute{
				Required: true,
			},
			"apply_state": schema.BoolAttribute{
				Required: true,
			},
		},
	}
}

func (r *GrainStringResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	// client, ok := req.ProviderData.(*http.Client)
	data, ok := req.ProviderData.(*providerData)

	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *http.Client, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)

		return
	}

	r.username = &data.Username
	r.privateKey = &data.PrivateKey
	r.uyuniBaseURL = &data.UyuniBaseURL
	r.uyuniUsername = &data.UyuniUsername
	r.uyuniPassword = &data.UyuniPassword
}

func (r *GrainStringResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data GrainStringResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	err := r.waitMinionIsUp(ctx, data)
	if err != nil {
		resp.Diagnostics.AddError(
			"failed to wait for the minion to be up",
			fmt.Sprintf("failed to wait for the minion %s to be up: %s", data.Server.ValueString(), err),
		)
		return
	}

	runCommand := fmt.Sprintf("/usr/lib/venv-salt-minion/bin/salt-call grains.setval %s %s", data.GrainKey.String(), data.GrainValue.String())
	_, err = r.runRemoteCommand(runCommand, ctx, data)
	if err != nil {
		resp.Diagnostics.AddError(
			"Cannot create the grain value on the Salt Minion",
			fmt.Sprintf("cannot create the grain value on theSalt Minion %s: %s", data.Server.ValueString(), err),
		)
		return
	}

	// For the purposes of this example code, hardcoding a response value to
	// save into the Terraform state.
	data.Id = types.StringValue(fmt.Sprintf("%s-%s", data.Server.ValueString(), data.GrainKey.ValueString()))

	// Write logs using the tflog package
	// Documentation: https://terraform.io/plugin/log
	tflog.Info(ctx, "created a resource")
	b, _ := json.MarshalIndent(data, "", "    ")
	tflog.Info(ctx, string(b))

	if data.ApplyState.ValueBool() {
		applyResult, err := r.applyState(ctx, data)
		if err != nil {
			resp.Diagnostics.AddError(
				err.Error(),
				err.Error())
		}
		resp.Diagnostics.AddWarning("state apply result", applyResult)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *GrainStringResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	tflog.Info(ctx, "READ called here")

	var data GrainStringResourceModel

	diags := req.State.Get(ctx, &data)
	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(diags...)

	if resp.Diagnostics.HasError() {
		return
	}

	err := r.waitMinionIsUp(ctx, data)
	if err != nil {
		resp.Diagnostics.AddError(
			"failed to wait for the minion to be up",
			fmt.Sprintf("failed to wait for the minion %s to be up: %s", data.Server.ValueString(), err),
		)
		return
	}

	runCommand := fmt.Sprintf("/usr/lib/venv-salt-minion/bin/salt-call grains.get %s --out=json", data.GrainKey.String())
	readGrain, err := r.runRemoteCommand(runCommand, ctx, data)
	if err != nil {
		resp.Diagnostics.AddError(
			"Cannot create the grain value on the Salt Minion",
			fmt.Sprintf("cannot create the grain value on theSalt Minion %s: %s", data.Server.ValueString(), err),
		)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Info(ctx, "readGrain, raw JSON:")
	tflog.Info(ctx, readGrain)

	liveGrains := SaltGrainStringModel{}
	_ = json.Unmarshal([]byte(readGrain), &liveGrains)

	tflog.Info(ctx, "decoded grains from JSON:")
	tflog.Info(ctx, liveGrains.Value)

	// if liveGrains.Value == nil {
	//	liveGrains.Value = ""
	// }

	strVal := types.StringValue(liveGrains.Value)
	data.GrainValue = strVal

	data.Id = types.StringValue(fmt.Sprintf("%s-%s", data.Server.ValueString(), data.GrainKey.ValueString()))

	tflog.Info(ctx, "read a resource")
	b, _ := json.MarshalIndent(data, "", "    ")
	tflog.Info(ctx, string(b))

	diags = resp.State.Set(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
}

func (r *GrainStringResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data GrainStringResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	err := r.waitMinionIsUp(ctx, data)
	if err != nil {
		resp.Diagnostics.AddError(
			"failed to wait for the minion to be up",
			fmt.Sprintf("failed to wait for the minion %s to be up: %s", data.Server.ValueString(), err),
		)
		return
	}

	runCommand := fmt.Sprintf("/usr/lib/venv-salt-minion/bin/salt-call grains.setval %s %s --out=json", data.GrainKey.String(), data.GrainValue.String())
	tflog.Info(ctx, runCommand)
	setGrain, err := r.runRemoteCommand(runCommand, ctx, data)
	if err != nil {
		resp.Diagnostics.AddError(
			"Cannot append the grain value on the Salt Minion",
			fmt.Sprintf("cannot append the grain value on theSalt Minion %s: %s", data.Server.ValueString(), err),
		)
	}
	tflog.Info(ctx, setGrain)
	if resp.Diagnostics.HasError() {
		return
	}

	if data.ApplyState.ValueBool() {
		applyResult, err := r.applyState(ctx, data)
		if err != nil {
			resp.Diagnostics.AddError(
				err.Error(),
				err.Error())
		}
		resp.Diagnostics.AddWarning("apply state result", applyResult)
		if resp.Diagnostics.HasError() {
			return
		}

	}

	data.Id = types.StringValue(fmt.Sprintf("%s-%s", data.Server.ValueString(), data.GrainKey.ValueString()))

	diags := resp.State.Set(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
}

func (r *GrainStringResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data GrainStringResourceModel

	// Read Terraform prior state data into the model
	// the existing data from state
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Info(ctx, "DELETE - Data from the state: ")
	tflog.Info(ctx, data.Server.String())
	tflog.Info(ctx, data.Id.String())
	tflog.Info(ctx, data.GrainKey.String())
	tflog.Info(ctx, data.GrainValue.String())

	err := r.waitMinionIsUp(ctx, data)
	if err != nil {
		resp.Diagnostics.AddError(
			"failed to wait for the minion to be up",
			fmt.Sprintf("failed to wait for the minion %s to be up: %s", data.Server.ValueString(), err),
		)
		return
	}

	runCommand := fmt.Sprintf("/usr/lib/venv-salt-minion/bin/salt-call grains.delkey %s --out=json", data.GrainKey.String())
	_, err = r.runRemoteCommand(runCommand, ctx, data)
	if err != nil {
		resp.Diagnostics.AddError(
			err.Error(),
			err.Error())
		return
	}

	if data.ApplyState.ValueBool() {
		applyResult, err := r.applyState(ctx, data)
		if err != nil {
			resp.Diagnostics.AddError(
				err.Error(),
				err.Error())
		}
		resp.Diagnostics.AddWarning("apply state result", applyResult)
		if resp.Diagnostics.HasError() {
			return
		}

	}
}

func (r *GrainStringResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *GrainStringResource) applyState(ctx context.Context, data GrainStringResourceModel) (string, error) {
	runCommand := "while true; do found=0; for f in /var/cache/venv-salt-minion/proc/*; do grep state.apply $f; if [ $? -eq 0 ]; then found=1; fi; done; if [ $found -eq 0 ]; then break; fi; sleep 1; done; /usr/lib/venv-salt-minion/bin/salt-call state.apply >> /var/log/state.apply.tf.log 2>&1"
	applyStateResult, err := r.runRemoteCommand(runCommand, ctx, data)
	if err != nil {
		return "", fmt.Errorf("cannot apply state: %s", err.Error())
	}

	return applyStateResult, nil
}

func (r *GrainStringResource) runRemoteCommand(runCommand string, ctx context.Context, data GrainStringResourceModel) (string, error) {
	signer, err := ssh.ParsePrivateKey([]byte(*r.privateKey))
	if err != nil {
		return "", fmt.Errorf("malformed private key: %s, please report this issue to the provider developers", err)
	}

	config := &ssh.ClientConfig{
		User: *r.username,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:22", data.Server.ValueString()), config)
	if err != nil {
		return "", fmt.Errorf("cannot connect to the Salt Minion %s: %s", data.Server.ValueString(), err)
	}

	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("cannot create session with the Salt Minion %s: %s", data.Server.ValueString(), err)
	}

	tflog.Info(ctx, runCommand)
	cmdOutput, err := session.Output(runCommand)
	tflog.Info(ctx, string(cmdOutput))

	if err != nil {
		return "", fmt.Errorf("cannot run the command %s on Salt Minion %s: %s", runCommand, data.Server.ValueString(), err)
	}

	return string(cmdOutput), nil
}

func (r *GrainStringResource) waitMinionIsUp(ctx context.Context, data GrainStringResourceModel) error {
	timeout := 30 * time.Minute
	deadline := time.Now().Add(timeout)

	tflog.Info(ctx, "starting to wait for the minion to be up")

	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout reached after %d minutes; salt-key for %s not accepted", timeout, data.Server.ValueString())
		}

		found, err := CheckServerAccepted(*r.uyuniBaseURL, *r.uyuniUsername, *r.uyuniPassword, data.Server.ValueString())
		if err != nil {
			return fmt.Errorf("error checking salt-key acceptance of %s: %s", data.Server.ValueString(), err)
		}

		tflog.Info(ctx, fmt.Sprintf("called checkServerAccepted with result: %v, error: %s", found, err))

		if found {
			return nil
		}
		time.Sleep(10 * time.Second)
	}
}
