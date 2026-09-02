// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	rtresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func newSsmSecureParameterValueDataSource(ssmFake *fakeSSMParameterGetter) *SsmSecureParameterValueDataSource {
	return &SsmSecureParameterValueDataSource{
		providerData: &ProviderData{Region: "us-east-1"},
		client:       ssmFake,
	}
}

// warningSummaries returns the summaries of a Read's warning diagnostics.
func warningSummaries(resp *datasource.ReadResponse) []string {
	var out []string
	for _, d := range resp.Diagnostics {
		if d.Severity() == diag.SeverityWarning {
			out = append(out, d.Summary())
		}
	}
	return out
}

func TestSsmSecureParameterValueDataSourceRead(t *testing.T) {
	t.Parallel()

	ssmFake := &fakeSSMParameterGetter{
		parameter: ssmParameter("/db/password", ssmTypeSecureString, "hunter2", 12),
	}
	d := newSsmSecureParameterValueDataSource(ssmFake)

	state, resp := readDataSource(t, d, SsmSecureParameterValueDataSourceModel{
		Name: types.StringValue("/db/password"),
	})
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %s", diagnosticsText(resp))
	}

	if got := state.Value.ValueString(); got != "hunter2" {
		t.Errorf("value = %q", got)
	}
	if got := state.Type.ValueString(); got != ssmTypeSecureString {
		t.Errorf("type = %q, want %q", got, ssmTypeSecureString)
	}
	if got := state.ResolvedVersion.ValueInt64(); got != 12 {
		t.Errorf("resolved_version = %d, want 12", got)
	}
	if !ssmFake.lastDecrypted {
		t.Error("GetParameter must be called WithDecryption for a SecureString")
	}

	warnings := warningSummaries(resp)
	if len(warnings) != 1 || warnings[0] != "Secret Value Is Stored in Terraform State" {
		t.Fatalf("expected exactly the state-exposure warning, got %v", warnings)
	}
	// The warning must actually explain the CloudFormation contrast and the
	// escape hatch, since it fires on every plan.
	text := diagnosticsText(resp)
	for _, want := range []string{
		"CloudFormation never stores a secure string value",
		"suppress_state_warning = true",
		"state encryption",
		"write-only",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("expected the warning to mention %q, got %s", want, text)
		}
	}
}

func TestSsmSecureParameterValueDataSourceSuppressStateWarning(t *testing.T) {
	t.Parallel()

	ssmFake := &fakeSSMParameterGetter{parameter: ssmParameter("/db/password", ssmTypeSecureString, "hunter2", 1)}
	d := newSsmSecureParameterValueDataSource(ssmFake)

	state, resp := readDataSource(t, d, SsmSecureParameterValueDataSourceModel{
		Name:                 types.StringValue("/db/password"),
		SuppressStateWarning: types.BoolValue(true),
	})
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %s", diagnosticsText(resp))
	}
	if got := warningSummaries(resp); len(got) != 0 {
		t.Fatalf("expected no warnings, got %v", got)
	}
	if !state.SuppressStateWarning.ValueBool() {
		t.Error("suppress_state_warning should be echoed back as true")
	}
}

// TestSsmSecureParameterValueDataSourceNonSecureStringWarns pins the
// deliberate asymmetry: reading a plaintext parameter through the sensitive
// data source is safe, so it warns rather than failing -- the reverse
// direction (a SecureString through the non-sensitive data source) is an
// error.
func TestSsmSecureParameterValueDataSourceNonSecureStringWarns(t *testing.T) {
	t.Parallel()

	for _, parameterType := range []string{ssmTypeString, ssmTypeStringList} {
		t.Run(parameterType, func(t *testing.T) {
			t.Parallel()

			ssmFake := &fakeSSMParameterGetter{parameter: ssmParameter("/a", parameterType, "plain", 1)}
			d := newSsmSecureParameterValueDataSource(ssmFake)

			state, resp := readDataSource(t, d, SsmSecureParameterValueDataSourceModel{
				Name: types.StringValue("/a"),
			})
			if resp.Diagnostics.HasError() {
				t.Fatalf("a non-SecureString must not fail the read: %s", diagnosticsText(resp))
			}
			if state.Value.ValueString() != "plain" {
				t.Errorf("value = %q, want the value to still be returned", state.Value.ValueString())
			}

			warnings := warningSummaries(resp)
			if len(warnings) != 2 {
				t.Fatalf("expected the not-a-SecureString warning and the state warning, got %v", warnings)
			}
			text := diagnosticsText(resp)
			if !strings.Contains(text, "Parameter Is Not a SecureString") {
				t.Errorf("expected a not-a-SecureString warning, got %s", text)
			}
			if !strings.Contains(text, "cfncompat_ssm_parameter_value") {
				t.Errorf("expected the warning to point at the non-sensitive data source, got %s", text)
			}
		})
	}
}

func TestSsmSecureParameterValueDataSourceSelectors(t *testing.T) {
	t.Parallel()

	ssmFake := &fakeSSMParameterGetter{parameter: ssmParameter("/a", ssmTypeSecureString, "s", 10)}
	d := newSsmSecureParameterValueDataSource(ssmFake)

	_, resp := readDataSource(t, d, SsmSecureParameterValueDataSourceModel{
		Name:    types.StringValue("/a"),
		Version: types.Int64Value(10),
	})
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %s", diagnosticsText(resp))
	}
	if ssmFake.lastSelector != "/a:10" {
		t.Fatalf("selector = %q, want %q", ssmFake.lastSelector, "/a:10")
	}
}

func TestSsmSecureParameterValueDataSourceErrors(t *testing.T) {
	t.Parallel()

	t.Run("GetParameter failure", func(t *testing.T) {
		t.Parallel()

		d := newSsmSecureParameterValueDataSource(&fakeSSMParameterGetter{err: errors.New("AccessDeniedException")})
		_, resp := readDataSource(t, d, SsmSecureParameterValueDataSourceModel{Name: types.StringValue("/a")})
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected the read to fail")
		}
		if text := diagnosticsText(resp); !strings.Contains(text, "ssm:GetParameter") {
			t.Fatalf("expected the diagnostic to name the permission, got %s", text)
		}
	})

	t.Run("ConfigErr", func(t *testing.T) {
		t.Parallel()

		d := &SsmSecureParameterValueDataSource{providerData: &ProviderData{ConfigErr: errors.New("no credentials")}}
		_, resp := readDataSource(t, d, SsmSecureParameterValueDataSourceModel{Name: types.StringValue("/a")})
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected the read to fail")
		}
		if text := diagnosticsText(resp); !strings.Contains(text, "no credentials") {
			t.Fatalf("expected the ConfigErr to be surfaced, got %s", text)
		}
	})
}

// TestAccSsmSecureParameterValueDataSource needs a SecureString parameter
// that already exists; the operator names it. Create and delete a throwaway
// one, for example:
//
//	aws ssm put-parameter --name /cfncompat/acctest/secure --type SecureString --value s3cret
//	aws ssm delete-parameter --name /cfncompat/acctest/secure
func TestAccSsmSecureParameterValueDataSource(t *testing.T) {
	if os.Getenv("CFNCOMPAT_TEST_AWS") != "1" {
		t.Skip("set CFNCOMPAT_TEST_AWS=1 (with TF_ACC=1 and AWS credentials) to run this acceptance test")
	}
	name := os.Getenv("CFNCOMPAT_TEST_SSM_SECURE_NAME")
	if name == "" {
		t.Skip("set CFNCOMPAT_TEST_SSM_SECURE_NAME to the name of a SecureString parameter to run this acceptance test")
	}

	rtresource.Test(t, rtresource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []rtresource.TestStep{
			{
				Config: `
data "cfncompat_ssm_secure_parameter_value" "secret" {
  name                   = "` + name + `"
  suppress_state_warning = true
}
`,
				Check: rtresource.ComposeAggregateTestCheckFunc(
					rtresource.TestCheckResourceAttrSet("data.cfncompat_ssm_secure_parameter_value.secret", "value"),
					rtresource.TestCheckResourceAttr("data.cfncompat_ssm_secure_parameter_value.secret", "type", "SecureString"),
					rtresource.TestCheckResourceAttrSet("data.cfncompat_ssm_secure_parameter_value.secret", "arn"),
					rtresource.TestCheckResourceAttrSet("data.cfncompat_ssm_secure_parameter_value.secret", "resolved_version"),
				),
			},
		},
	})
}
