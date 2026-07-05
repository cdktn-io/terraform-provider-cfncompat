// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// stringAttrValues converts plain strings into a slice of attr.Value, for
// building types.List/types.Set values in tests.
func stringAttrValues(values ...string) []attr.Value {
	out := make([]attr.Value, len(values))
	for i, v := range values {
		out[i] = types.StringValue(v)
	}
	return out
}

// emptyProviderModel returns the providerModel that results from an entirely
// empty `provider "cfncompat" {}` block: every attribute null, every nested
// block absent.
func emptyProviderModel() providerModel {
	return providerModel{
		AccessKey:              types.StringNull(),
		SecretKey:              types.StringNull(),
		Token:                  types.StringNull(),
		Profile:                types.StringNull(),
		Region:                 types.StringNull(),
		SharedConfigFiles:      types.ListNull(types.StringType),
		SharedCredentialsFiles: types.ListNull(types.StringType),
		MaxRetries:             types.Int64Null(),
		SkipMetadataAPICheck:   types.BoolNull(),
		HTTPProxy:              types.StringNull(),
		HTTPSProxy:             types.StringNull(),
		NoProxy:                types.StringNull(),
		Insecure:               types.BoolNull(),
		CustomResourceBucket:   types.StringNull(),
	}
}

func TestProviderModelToAWSBaseConfig(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("empty model applies defaults", func(t *testing.T) {
		t.Parallel()

		cfg, diags := providerModelToAWSBaseConfig(ctx, emptyProviderModel(), "test")
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}

		if cfg.MaxRetries != defaultMaxRetries {
			t.Errorf("MaxRetries = %d, want %d", cfg.MaxRetries, defaultMaxRetries)
		}
		if cfg.Region != "" {
			t.Errorf("Region = %q, want empty", cfg.Region)
		}
		if cfg.AccessKey != "" || cfg.SecretKey != "" || cfg.Token != "" {
			t.Errorf("expected no static credentials, got access=%q secret=%q token=%q", cfg.AccessKey, cfg.SecretKey, cfg.Token)
		}
		if !cfg.SkipCredsValidation {
			t.Errorf("SkipCredsValidation = false, want true (Configure must stay cheap/lenient)")
		}
		if cfg.AssumeRole != nil {
			t.Errorf("AssumeRole = %+v, want nil", cfg.AssumeRole)
		}
		if cfg.AssumeRoleWithWebIdentity != nil {
			t.Errorf("AssumeRoleWithWebIdentity = %+v, want nil", cfg.AssumeRoleWithWebIdentity)
		}
		if cfg.SharedConfigFiles != nil {
			t.Errorf("SharedConfigFiles = %v, want nil", cfg.SharedConfigFiles)
		}
		if cfg.SharedCredentialsFiles != nil {
			t.Errorf("SharedCredentialsFiles = %v, want nil", cfg.SharedCredentialsFiles)
		}
	})

	t.Run("static credentials, region and proxies map through", func(t *testing.T) {
		t.Parallel()

		model := emptyProviderModel()
		model.AccessKey = types.StringValue("AKIAEXAMPLE")
		model.SecretKey = types.StringValue("secret")
		model.Token = types.StringValue("token")
		model.Profile = types.StringValue("my-profile")
		model.Region = types.StringValue("us-west-2")
		model.MaxRetries = types.Int64Value(5)
		model.HTTPProxy = types.StringValue("http://proxy.example.com")
		model.HTTPSProxy = types.StringValue("https://proxy.example.com")
		model.NoProxy = types.StringValue("localhost")
		model.Insecure = types.BoolValue(true)

		sharedConfigFiles, diags := types.ListValue(types.StringType, stringAttrValues("config1", "config2"))
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics building shared_config_files: %v", diags)
		}
		model.SharedConfigFiles = sharedConfigFiles

		sharedCredentialsFiles, diags := types.ListValue(types.StringType, stringAttrValues("creds1"))
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics building shared_credentials_files: %v", diags)
		}
		model.SharedCredentialsFiles = sharedCredentialsFiles

		cfg, diags := providerModelToAWSBaseConfig(ctx, model, "test")
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}

		if cfg.AccessKey != "AKIAEXAMPLE" {
			t.Errorf("AccessKey = %q, want AKIAEXAMPLE", cfg.AccessKey)
		}
		if cfg.SecretKey != "secret" {
			t.Errorf("SecretKey = %q, want secret", cfg.SecretKey)
		}
		if cfg.Token != "token" {
			t.Errorf("Token = %q, want token", cfg.Token)
		}
		if cfg.Profile != "my-profile" {
			t.Errorf("Profile = %q, want my-profile", cfg.Profile)
		}
		if cfg.Region != "us-west-2" {
			t.Errorf("Region = %q, want us-west-2", cfg.Region)
		}
		if cfg.MaxRetries != 5 {
			t.Errorf("MaxRetries = %d, want 5", cfg.MaxRetries)
		}
		if cfg.HTTPProxy == nil || *cfg.HTTPProxy != "http://proxy.example.com" {
			t.Errorf("HTTPProxy = %v, want http://proxy.example.com", cfg.HTTPProxy)
		}
		if cfg.HTTPSProxy == nil || *cfg.HTTPSProxy != "https://proxy.example.com" {
			t.Errorf("HTTPSProxy = %v, want https://proxy.example.com", cfg.HTTPSProxy)
		}
		if cfg.NoProxy != "localhost" {
			t.Errorf("NoProxy = %q, want localhost", cfg.NoProxy)
		}
		if !cfg.Insecure {
			t.Errorf("Insecure = false, want true")
		}
		if len(cfg.SharedConfigFiles) != 2 || cfg.SharedConfigFiles[0] != "config1" || cfg.SharedConfigFiles[1] != "config2" {
			t.Errorf("SharedConfigFiles = %v, want [config1 config2]", cfg.SharedConfigFiles)
		}
		if len(cfg.SharedCredentialsFiles) != 1 || cfg.SharedCredentialsFiles[0] != "creds1" {
			t.Errorf("SharedCredentialsFiles = %v, want [creds1]", cfg.SharedCredentialsFiles)
		}
	})

	t.Run("skip_metadata_api_check maps to EC2MetadataServiceEnableState", func(t *testing.T) {
		t.Parallel()

		disabled := emptyProviderModel()
		disabled.SkipMetadataAPICheck = types.BoolValue(true)
		cfg, diags := providerModelToAWSBaseConfig(ctx, disabled, "test")
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if cfg.EC2MetadataServiceEnableState != imds.ClientDisabled {
			t.Errorf("EC2MetadataServiceEnableState = %v, want ClientDisabled", cfg.EC2MetadataServiceEnableState)
		}

		enabled := emptyProviderModel()
		enabled.SkipMetadataAPICheck = types.BoolValue(false)
		cfg, diags = providerModelToAWSBaseConfig(ctx, enabled, "test")
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if cfg.EC2MetadataServiceEnableState != imds.ClientEnabled {
			t.Errorf("EC2MetadataServiceEnableState = %v, want ClientEnabled", cfg.EC2MetadataServiceEnableState)
		}
	})

	t.Run("assume_role maps duration, arns, tags", func(t *testing.T) {
		t.Parallel()

		policyARNs, diags := types.ListValue(types.StringType, stringAttrValues("arn:aws:iam::123456789012:policy/example"))
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics building policy_arns: %v", diags)
		}
		tags, diags := types.MapValue(types.StringType, map[string]attr.Value{"env": types.StringValue("test")})
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics building tags: %v", diags)
		}
		transitiveTagKeys, diags := types.SetValue(types.StringType, stringAttrValues("env"))
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics building transitive_tag_keys: %v", diags)
		}

		model := emptyProviderModel()
		model.AssumeRole = &providerAssumeRoleModel{
			Duration:          types.StringValue("1h"),
			ExternalID:        types.StringValue("external-id"),
			Policy:            types.StringValue(`{"Version":"2012-10-17"}`),
			PolicyARNs:        policyARNs,
			RoleARN:           types.StringValue("arn:aws:iam::123456789012:role/example"),
			SessionName:       types.StringValue("session"),
			SourceIdentity:    types.StringValue("source"),
			Tags:              tags,
			TransitiveTagKeys: transitiveTagKeys,
		}

		cfg, diags := providerModelToAWSBaseConfig(ctx, model, "test")
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if len(cfg.AssumeRole) != 1 {
			t.Fatalf("AssumeRole = %+v, want exactly one entry", cfg.AssumeRole)
		}
		ar := cfg.AssumeRole[0]
		if ar.RoleARN != "arn:aws:iam::123456789012:role/example" {
			t.Errorf("RoleARN = %q", ar.RoleARN)
		}
		if ar.Duration != time.Hour {
			t.Errorf("Duration = %v, want 1h", ar.Duration)
		}
		if ar.ExternalID != "external-id" {
			t.Errorf("ExternalID = %q", ar.ExternalID)
		}
		if ar.SourceIdentity != "source" {
			t.Errorf("SourceIdentity = %q", ar.SourceIdentity)
		}
		if len(ar.PolicyARNs) != 1 || ar.PolicyARNs[0] != "arn:aws:iam::123456789012:policy/example" {
			t.Errorf("PolicyARNs = %v", ar.PolicyARNs)
		}
		if ar.Tags["env"] != "test" {
			t.Errorf("Tags = %v", ar.Tags)
		}
		if len(ar.TransitiveTagKeys) != 1 || ar.TransitiveTagKeys[0] != "env" {
			t.Errorf("TransitiveTagKeys = %v", ar.TransitiveTagKeys)
		}
	})

	t.Run("assume_role invalid duration errors", func(t *testing.T) {
		t.Parallel()

		model := emptyProviderModel()
		model.AssumeRole = &providerAssumeRoleModel{
			Duration:          types.StringValue("not-a-duration"),
			ExternalID:        types.StringNull(),
			Policy:            types.StringNull(),
			PolicyARNs:        types.ListNull(types.StringType),
			RoleARN:           types.StringValue("arn:aws:iam::123456789012:role/example"),
			SessionName:       types.StringNull(),
			SourceIdentity:    types.StringNull(),
			Tags:              types.MapNull(types.StringType),
			TransitiveTagKeys: types.SetNull(types.StringType),
		}

		_, diags := providerModelToAWSBaseConfig(ctx, model, "test")
		if !diags.HasError() {
			t.Fatal("expected an error diagnostic for an invalid duration, got none")
		}
	})

	t.Run("assume_role_with_web_identity maps through", func(t *testing.T) {
		t.Parallel()

		model := emptyProviderModel()
		model.AssumeRoleWithWebIdentity = &providerAssumeRoleWithWebIdentityModel{
			Duration:             types.StringValue("30m"),
			Policy:               types.StringNull(),
			PolicyARNs:           types.ListNull(types.StringType),
			RoleARN:              types.StringValue("arn:aws:iam::123456789012:role/example"),
			SessionName:          types.StringValue("session"),
			WebIdentityToken:     types.StringValue("token"),
			WebIdentityTokenFile: types.StringNull(),
		}

		cfg, diags := providerModelToAWSBaseConfig(ctx, model, "test")
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if cfg.AssumeRoleWithWebIdentity == nil {
			t.Fatal("AssumeRoleWithWebIdentity = nil, want non-nil")
		}
		if cfg.AssumeRoleWithWebIdentity.RoleARN != "arn:aws:iam::123456789012:role/example" {
			t.Errorf("RoleARN = %q", cfg.AssumeRoleWithWebIdentity.RoleARN)
		}
		if cfg.AssumeRoleWithWebIdentity.Duration != 30*time.Minute {
			t.Errorf("Duration = %v, want 30m", cfg.AssumeRoleWithWebIdentity.Duration)
		}
		if cfg.AssumeRoleWithWebIdentity.WebIdentityToken != "token" {
			t.Errorf("WebIdentityToken = %q", cfg.AssumeRoleWithWebIdentity.WebIdentityToken)
		}
	})

	t.Run("endpoints.sts maps to StsEndpoint", func(t *testing.T) {
		t.Parallel()

		model := emptyProviderModel()
		model.Endpoints = &providerEndpointsModel{
			Lambda: types.StringValue("http://localhost:4566"),
			SNS:    types.StringValue("http://localhost:4566"),
			S3:     types.StringValue("http://localhost:4566"),
			STS:    types.StringValue("http://localhost:4566"),
		}

		cfg, diags := providerModelToAWSBaseConfig(ctx, model, "test")
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if cfg.StsEndpoint != "http://localhost:4566" {
			t.Errorf("StsEndpoint = %q, want http://localhost:4566", cfg.StsEndpoint)
		}
	})
}

func TestProviderEndpointsModelToEndpointsConfig(t *testing.T) {
	t.Parallel()

	var nilModel *providerEndpointsModel
	if got := nilModel.toEndpointsConfig(); got != (EndpointsConfig{}) {
		t.Errorf("nil endpoints model = %+v, want zero value", got)
	}

	model := &providerEndpointsModel{
		Lambda: types.StringValue("http://lambda.example"),
		SNS:    types.StringValue("http://sns.example"),
		S3:     types.StringValue("http://s3.example"),
		STS:    types.StringValue("http://sts.example"),
	}
	want := EndpointsConfig{
		Lambda: "http://lambda.example",
		SNS:    "http://sns.example",
		S3:     "http://s3.example",
		STS:    "http://sts.example",
	}
	if got := model.toEndpointsConfig(); got != want {
		t.Errorf("toEndpointsConfig() = %+v, want %+v", got, want)
	}
}

// TestProviderConfigureEmptyConfigSucceeds verifies that an entirely empty
// `provider "cfncompat" {}` block Configures successfully with no error
// diagnostics, even with no AWS credentials or region resolvable from the
// environment. ConfigErr on the resulting ProviderData may be set (that is
// expected and is how the "lenient Configure" contract surfaces failures to
// resources later), but Configure itself must never fail.
func TestProviderConfigureEmptyConfigSucceeds(t *testing.T) {
	// Isolate from the developer/CI environment's ambient AWS configuration
	// so this test exercises the true "nothing configured anywhere" case.
	emptyDir := t.TempDir()
	t.Setenv("HOME", emptyDir)
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(emptyDir, "does-not-exist-credentials"))
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(emptyDir, "does-not-exist-config"))
	// Keep this test fast and network-free: don't let it try to reach the
	// EC2 Instance Metadata Service.
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	ctx := context.Background()

	p := &CfncompatProvider{version: "test"}

	schemaResp := &provider.SchemaResponse{}
	p.Schema(ctx, provider.SchemaRequest{}, schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics building schema: %v", schemaResp.Diagnostics)
	}

	tfType := schemaResp.Schema.Type().TerraformType(ctx)
	objType, ok := tfType.(tftypes.Object)
	if !ok {
		t.Fatalf("schema type is not an Object: %T", tfType)
	}
	values := make(map[string]tftypes.Value, len(objType.AttributeTypes))
	for name, attrType := range objType.AttributeTypes {
		values[name] = tftypes.NewValue(attrType, nil)
	}
	raw := tftypes.NewValue(objType, values)

	configureReq := provider.ConfigureRequest{
		TerraformVersion: "1.8.0",
		Config: tfsdk.Config{
			Raw:    raw,
			Schema: schemaResp.Schema,
		},
	}
	configureResp := &provider.ConfigureResponse{}

	p.Configure(ctx, configureReq, configureResp)

	if configureResp.Diagnostics.HasError() {
		t.Fatalf("Configure() produced error diagnostics for an empty provider block: %v", configureResp.Diagnostics)
	}

	pd, ok := configureResp.ResourceData.(*ProviderData)
	if !ok {
		t.Fatalf("ResourceData is not *ProviderData: %T", configureResp.ResourceData)
	}
	if configureResp.DataSourceData != configureResp.ResourceData {
		t.Errorf("DataSourceData and ResourceData should be the same ProviderData instance")
	}
	if pd.CustomResourceBucket != "" {
		t.Errorf("CustomResourceBucket = %q, want empty", pd.CustomResourceBucket)
	}
	if pd.Endpoints != (EndpointsConfig{}) {
		t.Errorf("Endpoints = %+v, want zero value", pd.Endpoints)
	}
	// ConfigErr is deliberately not asserted either way: depending on the
	// test host it may or may not resolve a default credentials chain (e.g.
	// CI runners with an instance role). Both outcomes are valid so long as
	// Configure() itself did not fail.
	t.Logf("ConfigErr = %v", pd.ConfigErr)
}
