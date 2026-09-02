// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/hashicorp/terraform-plugin-framework/types"
	rtresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// fakeSecretsManagerGetter is a secretsManagerGetter fake.
type fakeSecretsManagerGetter struct {
	out *secretsmanager.GetSecretValueOutput
	err error

	calls           int
	lastSecretID    string
	lastVersionID   string
	lastVersionStag string
}

func (f *fakeSecretsManagerGetter) GetSecretValue(_ context.Context, in *secretsmanager.GetSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	f.calls++
	f.lastSecretID = aws.ToString(in.SecretId)
	f.lastVersionID = aws.ToString(in.VersionId)
	f.lastVersionStag = aws.ToString(in.VersionStage)
	if f.err != nil {
		return nil, f.err
	}
	return f.out, nil
}

// secretValueOutput builds a GetSecretValue result for the fake.
func secretValueOutput(secretString string) *secretsmanager.GetSecretValueOutput {
	created := time.Date(2026, 7, 15, 9, 30, 0, 0, time.UTC)
	return &secretsmanager.GetSecretValueOutput{
		ARN:           aws.String("arn:aws:secretsmanager:us-east-1:123456789012:secret:prod/db-AbCdEf"),
		Name:          aws.String("prod/db"),
		SecretString:  aws.String(secretString),
		VersionId:     aws.String("01234567-89ab-cdef-0123-456789abcdef"),
		VersionStages: []string{"AWSCURRENT"},
		CreatedDate:   &created,
	}
}

func newSecretsManagerSecretValueDataSource(fake *fakeSecretsManagerGetter) *SecretsManagerSecretValueDataSource {
	return &SecretsManagerSecretValueDataSource{
		providerData: &ProviderData{Region: "us-east-1"},
		client:       fake,
	}
}

// secretConfig is the base config model for a read.
func secretConfig(secretID string) SecretsManagerSecretValueDataSourceModel {
	return SecretsManagerSecretValueDataSourceModel{
		SecretID:      types.StringValue(secretID),
		VersionStages: types.ListNull(types.StringType),
	}
}

func TestSecretsManagerSecretValueDataSourceRead(t *testing.T) {
	t.Parallel()

	fake := &fakeSecretsManagerGetter{out: secretValueOutput(`{"username":"admin","password":"hunter2"}`)}
	d := newSecretsManagerSecretValueDataSource(fake)

	state, resp := readDataSource(t, d, secretConfig("prod/db"))
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %s", diagnosticsText(resp))
	}

	// With no json_key the whole SecretString is the value.
	if got := state.Value.ValueString(); got != `{"username":"admin","password":"hunter2"}` {
		t.Errorf("value = %q, want the whole SecretString", got)
	}
	if got := state.Name.ValueString(); got != "prod/db" {
		t.Errorf("name = %q", got)
	}
	if got := state.ResolvedVersionID.ValueString(); got != "01234567-89ab-cdef-0123-456789abcdef" {
		t.Errorf("resolved_version_id = %q", got)
	}
	if got := stateValues(t, state.VersionStages); !equalStrings(got, []string{"AWSCURRENT"}) {
		t.Errorf("version_stages = %v", got)
	}
	if got := state.CreatedDate.ValueString(); got != "2026-07-15T09:30:00Z" {
		t.Errorf("created_date = %q, want an RFC 3339 UTC timestamp", got)
	}
	if got, want := state.Id.ValueString(), state.Arn.ValueString(); got != want {
		t.Errorf("id = %q, want the secret ARN %q", got, want)
	}
	if warnings := warningSummaries(resp); len(warnings) != 1 || warnings[0] != "Secret Value Is Stored in Terraform State" {
		t.Errorf("expected exactly the state-exposure warning, got %v", warnings)
	}
	if fake.lastVersionID != "" || fake.lastVersionStag != "" {
		t.Errorf("expected no version selection to be sent (AWSCURRENT is the API default), got id=%q stage=%q",
			fake.lastVersionID, fake.lastVersionStag)
	}
}

func TestSecretsManagerSecretValueDataSourceJSONKey(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		secretString  string
		jsonKey       string
		want          string
		errorContains string
	}{
		"string value": {
			secretString: `{"username":"admin","password":"hunter2"}`,
			jsonKey:      "password", want: "hunter2",
		},
		"integer value keeps its exact rendering": {
			secretString: `{"port":5432}`, jsonKey: "port", want: "5432",
		},
		"float value keeps its exact rendering": {
			secretString: `{"ratio":0.50}`, jsonKey: "ratio", want: "0.50",
		},
		"boolean value": {
			secretString: `{"enabled":true}`, jsonKey: "enabled", want: "true",
		},
		"missing key": {
			secretString: `{"username":"admin"}`, jsonKey: "password",
			errorContains: `has no key "password"`,
		},
		"secret is not JSON": {
			secretString: `plain-text-secret`, jsonKey: "password",
			errorContains: "must be a JSON object",
		},
		"secret is a JSON array, not an object": {
			secretString: `["a","b"]`, jsonKey: "password",
			errorContains: "must be a JSON object",
		},
		"key holds an object": {
			secretString: `{"nested":{"a":1}}`, jsonKey: "nested",
			errorContains: "is a JSON object",
		},
		"key holds an array": {
			secretString: `{"list":[1,2]}`, jsonKey: "list",
			errorContains: "is a JSON array",
		},
		"key holds null": {
			secretString: `{"nothing":null}`, jsonKey: "nothing",
			errorContains: "is a null",
		},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newSecretsManagerSecretValueDataSource(&fakeSecretsManagerGetter{out: secretValueOutput(testCase.secretString)})
			config := secretConfig("prod/db")
			config.JSONKey = types.StringValue(testCase.jsonKey)

			state, resp := readDataSource(t, d, config)

			if testCase.errorContains != "" {
				if !resp.Diagnostics.HasError() {
					t.Fatal("expected the read to fail")
				}
				if text := diagnosticsText(resp); !strings.Contains(text, testCase.errorContains) {
					t.Fatalf("expected diagnostics to contain %q, got %s", testCase.errorContains, text)
				}
				return
			}

			if resp.Diagnostics.HasError() {
				t.Fatalf("unexpected diagnostics: %s", diagnosticsText(resp))
			}
			if got := state.Value.ValueString(); got != testCase.want {
				t.Fatalf("value = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestSecretsManagerSecretValueDataSourceVersionSelection(t *testing.T) {
	t.Parallel()

	// The Secrets Manager API accepts both a version id and a stage, and
	// requires them to agree; this data source passes both through unchanged
	// rather than pre-empting the API's rule.
	fake := &fakeSecretsManagerGetter{out: secretValueOutput("s")}
	d := newSecretsManagerSecretValueDataSource(fake)

	config := secretConfig("prod/db")
	config.VersionStage = types.StringValue("AWSPREVIOUS")
	config.VersionID = types.StringValue("v1")

	_, resp := readDataSource(t, d, config)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %s", diagnosticsText(resp))
	}
	if fake.lastVersionStag != "AWSPREVIOUS" || fake.lastVersionID != "v1" {
		t.Fatalf("expected both selectors to be sent, got stage=%q id=%q", fake.lastVersionStag, fake.lastVersionID)
	}
}

func TestSecretsManagerSecretValueDataSourceSecretBinary(t *testing.T) {
	t.Parallel()

	out := secretValueOutput("")
	out.SecretString = nil
	out.SecretBinary = []byte{0x01, 0x02}

	d := newSecretsManagerSecretValueDataSource(&fakeSecretsManagerGetter{out: out})
	_, resp := readDataSource(t, d, secretConfig("prod/binary"))
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected a SecretBinary-only secret to fail the read")
	}
	if text := diagnosticsText(resp); !strings.Contains(text, "SecretString only") {
		t.Fatalf("expected the diagnostic to explain the SecretString-only contract, got %s", text)
	}
}

func TestSecretsManagerSecretValueDataSourceErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err           error
		errorContains string
	}{
		"secret not found": {
			&smtypes.ResourceNotFoundException{},
			"does not exist in this account and region",
		},
		"invalid request": {
			&smtypes.InvalidRequestException{},
			"scheduled for deletion",
		},
		"anything else names the permission": {
			errors.New("AccessDeniedException"),
			"secretsmanager:GetSecretValue",
		},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newSecretsManagerSecretValueDataSource(&fakeSecretsManagerGetter{err: testCase.err})
			_, resp := readDataSource(t, d, secretConfig("prod/db"))
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected the read to fail")
			}
			if text := diagnosticsText(resp); !strings.Contains(text, testCase.errorContains) {
				t.Fatalf("expected diagnostics to contain %q, got %s", testCase.errorContains, text)
			}
		})
	}
}

func TestSecretsManagerSecretValueDataSourceSuppressStateWarning(t *testing.T) {
	t.Parallel()

	d := newSecretsManagerSecretValueDataSource(&fakeSecretsManagerGetter{out: secretValueOutput("s")})
	config := secretConfig("prod/db")
	config.SuppressStateWarning = types.BoolValue(true)

	_, resp := readDataSource(t, d, config)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %s", diagnosticsText(resp))
	}
	if got := warningSummaries(resp); len(got) != 0 {
		t.Fatalf("expected no warnings, got %v", got)
	}
}

func TestSecretsManagerSecretValueDataSourceNotConfigured(t *testing.T) {
	t.Parallel()

	t.Run("ConfigErr", func(t *testing.T) {
		t.Parallel()

		d := &SecretsManagerSecretValueDataSource{providerData: &ProviderData{ConfigErr: errors.New("no credentials")}}
		_, resp := readDataSource(t, d, secretConfig("prod/db"))
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected the read to fail")
		}
		if text := diagnosticsText(resp); !strings.Contains(text, "no credentials") {
			t.Fatalf("expected the ConfigErr to be surfaced, got %s", text)
		}
	})

	t.Run("no client", func(t *testing.T) {
		t.Parallel()

		d := &SecretsManagerSecretValueDataSource{providerData: &ProviderData{}}
		_, resp := readDataSource(t, d, secretConfig("prod/db"))
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected the read to fail")
		}
		if text := diagnosticsText(resp); !strings.Contains(text, "bug in the cfncompat provider") {
			t.Fatalf("expected a provider-bug diagnostic, got %s", text)
		}
	})
}

// TestAccSecretsManagerSecretValueDataSource needs a secret that already
// exists; the operator names it, and it must hold a JSON SecretString with a
// "password" key. Create and delete a throwaway one, for example:
//
//	aws secretsmanager create-secret --name cfncompat-acctest/db \
//	  --secret-string '{"username":"admin","password":"s3cret"}'
//	aws secretsmanager delete-secret --secret-id cfncompat-acctest/db \
//	  --force-delete-without-recovery
func TestAccSecretsManagerSecretValueDataSource(t *testing.T) {
	if os.Getenv("CFNCOMPAT_TEST_AWS") != "1" {
		t.Skip("set CFNCOMPAT_TEST_AWS=1 (with TF_ACC=1 and AWS credentials) to run this acceptance test")
	}
	secretID := os.Getenv("CFNCOMPAT_TEST_SECRET_ID")
	if secretID == "" {
		t.Skip("set CFNCOMPAT_TEST_SECRET_ID to the name or ARN of a JSON secret to run this acceptance test")
	}

	rtresource.Test(t, rtresource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []rtresource.TestStep{
			{
				Config: `
data "cfncompat_secretsmanager_secret_value" "whole" {
  secret_id              = "` + secretID + `"
  suppress_state_warning = true
}

data "cfncompat_secretsmanager_secret_value" "password" {
  secret_id              = "` + secretID + `"
  json_key               = "password"
  suppress_state_warning = true
}
`,
				Check: rtresource.ComposeAggregateTestCheckFunc(
					rtresource.TestCheckResourceAttrSet("data.cfncompat_secretsmanager_secret_value.whole", "value"),
					rtresource.TestCheckResourceAttrSet("data.cfncompat_secretsmanager_secret_value.whole", "arn"),
					rtresource.TestCheckResourceAttrSet("data.cfncompat_secretsmanager_secret_value.whole", "name"),
					rtresource.TestCheckResourceAttrSet("data.cfncompat_secretsmanager_secret_value.whole", "resolved_version_id"),
					rtresource.TestCheckResourceAttr("data.cfncompat_secretsmanager_secret_value.whole", "version_stages.0", "AWSCURRENT"),
					rtresource.TestCheckResourceAttrSet("data.cfncompat_secretsmanager_secret_value.password", "value"),
					rtresource.TestCheckResourceAttrPair(
						"data.cfncompat_secretsmanager_secret_value.whole", "arn",
						"data.cfncompat_secretsmanager_secret_value.password", "arn",
					),
				),
			},
		},
	})
}
