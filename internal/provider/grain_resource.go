// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"golang.org/x/crypto/ssh"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &GrainResource{}
var _ resource.ResourceWithImportState = &GrainResource{}

func NewGrainResource() resource.Resource {
	return &GrainResource{}
}

// GrainResource defines the resource implementation.
type GrainResource struct {
	username      *string
	privateKey    *string
	uyuniBaseURL  *string
	uyuniUsername *string
	uyuniPassword *string
}

// GrainResourceModel describes the resource data model.
type GrainResourceModel struct {
	Id         types.String `tfsdk:"id"`
	Server     types.String `tfsdk:"server"`
	GrainKey   types.String `tfsdk:"grain_key"`
	GrainValue types.List   `tfsdk:"grain_value"`
	ApplyState types.Bool   `tfsdk:"apply_state"`
}

type SaltGrainModel struct {
	Roles []string `json:"local"`
}

func (r *GrainResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_grain"
}

func (r *GrainResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		// This description is used by the documentation generator and the language server.
		MarkdownDescription: "Example resource",

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
			"grain_value": schema.ListAttribute{
				ElementType: types.StringType,
				Required:    true,
			},
			"apply_state": schema.BoolAttribute{
				Required: true,
			},
		},
	}
}

func (r *GrainResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *GrainResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data GrainResourceModel

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

	for _, value := range data.GrainValue.Elements() {
		runCommand := fmt.Sprintf("/usr/lib/venv-salt-minion/bin/salt-call grains.append %s %s", data.GrainKey.String(), value.String())
		_, err := r.runRemoteCommand(runCommand, ctx, data)
		if err != nil {
			resp.Diagnostics.AddError(
				"Cannot create the grain value on the Salt Minion",
				fmt.Sprintf("cannot create the grain value on theSalt Minion %s: %s", data.Server.ValueString(), err),
			)
		}
		if resp.Diagnostics.HasError() {
			return
		}
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
		applyStateResult, err := r.applyState(ctx, data)
		if err != nil {
			resp.Diagnostics.AddError(
				err.Error(),
				err.Error())
		}
		resp.Diagnostics.AddWarning("apply state result", applyStateResult)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *GrainResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	tflog.Info(ctx, "READ called here")

	var data GrainResourceModel

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

	liveGrains := SaltGrainModel{}
	_ = json.Unmarshal([]byte(readGrain), &liveGrains)

	tflog.Info(ctx, "decoded grains from JSON:")
	for _, role := range liveGrains.Roles {
		tflog.Info(ctx, role)
	}

	if liveGrains.Roles == nil {
		liveGrains.Roles = []string{}
	}

	var grainItems []attr.Value
	for _, item := range liveGrains.Roles {
		grainItems = append(grainItems, types.StringValue(item))
	}

	listVal, diags := types.ListValue(types.StringType, grainItems)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.GrainValue = listVal

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

func (r *GrainResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data GrainResourceModel

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

	runCommand := fmt.Sprintf("/usr/lib/venv-salt-minion/bin/salt-call grains.get %s --out=json", data.GrainKey.String())
	readGrain, err := r.runRemoteCommand(runCommand, ctx, data)
	if err != nil {
		resp.Diagnostics.AddError(
			"Cannot get the grain value on the Salt Minion",
			fmt.Sprintf("cannot get the grain value on theSalt Minion %s: %s", data.Server.ValueString(), err),
		)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Info(ctx, "readGrain, raw JSON:")
	tflog.Info(ctx, readGrain)

	liveGrains := SaltGrainModel{}
	err = json.Unmarshal([]byte(readGrain), &liveGrains)
	if err != nil {
		return
	}

	// porovnam state s tim co je v grains a smazu to, co tam byt nema

	tflog.Info(ctx, "UPDATE called here")
	tflog.Info(ctx, fmt.Sprintf("%v", data.GrainValue.Elements()))
	tflog.Info(ctx, fmt.Sprintf("%v", liveGrains.Roles))
	tflog.Info(ctx, "===============")

	var grainValueStr types.String
	var ok bool

	for _, grainValue := range data.GrainValue.Elements() {
		if grainValueStr, ok = grainValue.(types.String); !ok {
			resp.Diagnostics.AddError(
				"cannot convert grain to String, type conversion failed",
				fmt.Sprintf("cannot convert grain to String, type conversion failed: %s", err),
			)
			return
		}

		isFound := false
		for _, stateGrainValue := range liveGrains.Roles {
			tflog.Info(ctx, fmt.Sprintf("COMPARING: %s and %s", grainValueStr.ValueString(), stateGrainValue))
			if grainValueStr.ValueString() == stateGrainValue {
				isFound = true
			}
		}
		if !isFound {
			// if not found, the grain needs to be added

			runCommand := fmt.Sprintf("/usr/lib/venv-salt-minion/bin/salt-call grains.append %s %s --out=json", data.GrainKey.String(), grainValue)
			tflog.Info(ctx, runCommand)
			appendGrain, err := r.runRemoteCommand(runCommand, ctx, data)
			if err != nil {
				resp.Diagnostics.AddError(
					"Cannot append the grain value on the Salt Minion",
					fmt.Sprintf("cannot append the grain value on theSalt Minion %s: %s", data.Server.ValueString(), err),
				)
			}
			tflog.Info(ctx, appendGrain)
			if resp.Diagnostics.HasError() {
				return
			}
		}
	}

	// update grains from what is now on the minion side
	runCommand = fmt.Sprintf("/usr/lib/venv-salt-minion/bin/salt-call grains.get %s --out=json", data.GrainKey.String())
	readGrain, err = r.runRemoteCommand(runCommand, ctx, data)
	if err != nil {
		resp.Diagnostics.AddError(
			"Cannot get the grain value on the Salt Minion",
			fmt.Sprintf("cannot get the grain value on theSalt Minion %s: %s", data.Server.ValueString(), err),
		)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	liveGrains = SaltGrainModel{}
	err = json.Unmarshal([]byte(readGrain), &liveGrains)
	if err != nil {
		return
	}
	tflog.Info(ctx, fmt.Sprintf("AKTUALIZOVANE HODNOTY GRAINS Z MINIONA: %v", liveGrains))

	// porovnam grains se statem a pridam to, co v nem neni
	for _, stateGrainValue := range liveGrains.Roles {
		isFound := false
		for _, grainValue := range data.GrainValue.Elements() {
			if grainValueStr, ok = grainValue.(types.String); !ok {
				resp.Diagnostics.AddError(
					"cannot convert grain to String, type conversion failed",
					fmt.Sprintf("cannot convert grain to String, type conversion failed: %s", err),
				)
				return
			}

			tflog.Info(ctx, "hodnoty pro porovnavani:")
			tflog.Info(ctx, stateGrainValue)
			tflog.Info(ctx, grainValue.String())

			if stateGrainValue == grainValueStr.ValueString() {
				isFound = true
			}
		}
		if !isFound {
			// tento grain se musi na minionovi smazat

			runCommand = fmt.Sprintf("/usr/lib/venv-salt-minion/bin/salt-call grains.remove %s %s --out=json", data.GrainKey.String(), stateGrainValue)
			tflog.Info(ctx, runCommand)
			appendGrain, err := r.runRemoteCommand(runCommand, ctx, data)
			if err != nil {
				resp.Diagnostics.AddError(
					"Cannot delete the grain value on the Salt Minion",
					fmt.Sprintf("cannot delete the grain value on theSalt Minion %s: %s", data.Server.ValueString(), err),
				)
			}
			tflog.Info(ctx, appendGrain)
			if resp.Diagnostics.HasError() {
				return
			}
		}
	}

	if data.ApplyState.ValueBool() {
		applyStateResult, err := r.applyState(ctx, data)
		if err != nil {
			resp.Diagnostics.AddError(
				err.Error(),
				err.Error())
		}
		resp.Diagnostics.AddWarning("apply state result", applyStateResult)
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

func (r *GrainResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data GrainResourceModel

	// Read Terraform prior state data into the model
	// the existing data from state
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

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

	tflog.Info(ctx, "DELETE - Data from the state: ")
	tflog.Info(ctx, data.Server.String())
	tflog.Info(ctx, data.Id.String())
	tflog.Info(ctx, data.GrainKey.String())
	tflog.Info(ctx, data.GrainValue.String())

	for _, grainValue := range data.GrainValue.Elements() {
		runCommand := fmt.Sprintf("/usr/lib/venv-salt-minion/bin/salt-call grains.remove %s %s --out=json", data.GrainKey.String(), grainValue)
		_, err := r.runRemoteCommand(runCommand, ctx, data)
		if err != nil {
			resp.Diagnostics.AddError(
				err.Error(),
				err.Error())
		}
		if resp.Diagnostics.HasError() {
			return
		}
	}

	if data.ApplyState.ValueBool() {
		applyStateResult, err := r.applyState(ctx, data)
		if err != nil {
			resp.Diagnostics.AddError(
				err.Error(),
				err.Error())
		}
		resp.Diagnostics.AddWarning("apply state result", applyStateResult)
		if resp.Diagnostics.HasError() {
			return
		}
	}
}

func (r *GrainResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *GrainResource) applyState(ctx context.Context, data GrainResourceModel) (string, error) {
	runCommand := "while true; do found=0; for f in /var/cache/venv-salt-minion/proc/*; do grep state.apply $f; if [ $? -eq 0 ]; then found=1; fi; done; if [ $found -eq 0 ]; then break; fi; sleep 1; done; /usr/lib/venv-salt-minion/bin/salt-call state.apply >> /var/log/state.apply.tf.log 2>&1"
	applyStateResult, err := r.runRemoteCommand(runCommand, ctx, data)
	if err != nil {
		return applyStateResult, fmt.Errorf("cannot apply state: %s", err.Error())
	}

	return applyStateResult, nil
}

func (r *GrainResource) waitMinionIsUp(ctx context.Context, data GrainResourceModel) error {
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

	// runCommand := "while [ ! -f /etc/venv-salt-minion/pki/minion/minion_master.pub ]; do sleep 1; done"
	// _, err := r.runRemoteCommand(runCommand, ctx, data)
	// if err != nil {
	// 		return fmt.Errorf("failed to wait for the minion to be up: %s", err.Error())
	// }
	// return nil
}

func (r *GrainResource) runRemoteCommand(runCommand string, ctx context.Context, data GrainResourceModel) (string, error) {
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

// CheckServerAccepted logs in and checks if a server is in the accepted list.
func CheckServerAccepted(baseURL, username, password, serverName string) (bool, error) {
	// Create HTTP client with cookie jar
	jar, err := cookiejar.New(nil)
	if err != nil {
		return false, fmt.Errorf("failed to create cookie jar: %w", err)
	}
	client := &http.Client{
		Jar: jar,
	}

	// Login payload
	loginPayload := map[string]string{
		"login":    username,
		"password": password,
	}
	payloadBytes, err := json.Marshal(loginPayload)
	if err != nil {
		return false, fmt.Errorf("failed to marshal login payload: %w", err)
	}

	// Perform login request
	loginURL := fmt.Sprintf("%s/auth/login", strings.TrimRight(baseURL, "/"))
	req, err := http.NewRequest("POST", loginURL, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return false, fmt.Errorf("failed to create login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Skip TLS verification
	client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}

	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("login failed: %s", string(body))
	}

	// Fetch accepted list
	acceptedListURL := fmt.Sprintf("%s/saltkey/acceptedList", strings.TrimRight(baseURL, "/"))
	req, err = http.NewRequest("GET", acceptedListURL, nil)
	if err != nil {
		return false, fmt.Errorf("failed to create acceptedList request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err = client.Do(req)
	if err != nil {
		return false, fmt.Errorf("acceptedList request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("failed to fetch acceptedList: %s", string(body))
	}

	// Parse the result
	var result struct {
		Success bool     `json:"success"`
		Result  []string `json:"result"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("failed to parse acceptedList response: %w", err)
	}

	// Check if the server is in the list
	for _, s := range result.Result {
		if s == serverName {
			return true, nil
		}
	}
	return false, nil
}
