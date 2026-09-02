// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// --- fakes shared by every SSM/Secrets Manager data source test -------------

// fakeSSMParameterGetter is an ssmParameterGetter fake. It answers with the
// configured parameter (or error) and records the selector it was asked for,
// so the name:version / name:label selector syntax can be asserted.
type fakeSSMParameterGetter struct {
	parameter *ssmtypes.Parameter
	err       error

	calls         int
	lastSelector  string
	lastDecrypted bool
	// nilParameter makes GetParameter answer successfully with no parameter,
	// the malformed response the data sources must not panic on.
	nilParameter bool
}

func (f *fakeSSMParameterGetter) GetParameter(_ context.Context, params *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	f.calls++
	f.lastSelector = aws.ToString(params.Name)
	f.lastDecrypted = aws.ToBool(params.WithDecryption)
	if f.err != nil {
		return nil, f.err
	}
	if f.nilParameter {
		return &ssm.GetParameterOutput{}, nil
	}
	return &ssm.GetParameterOutput{Parameter: f.parameter}, nil
}

// ssmParameter builds a GetParameter result for the fake.
func ssmParameter(name, parameterType, value string, version int64) *ssmtypes.Parameter {
	modified := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	return &ssmtypes.Parameter{
		ARN:              aws.String("arn:aws:ssm:us-east-1:123456789012:parameter" + name),
		Name:             aws.String(name),
		Type:             ssmtypes.ParameterType(parameterType),
		DataType:         aws.String("text"),
		Value:            aws.String(value),
		Version:          version,
		LastModifiedDate: &modified,
	}
}

// fakeCFNValidationAPI implements both cfnEC2ValidationAPI and
// cfnRoute53ValidationAPI. Each Describe* answers with the ids/names it has
// been told exist, so a value that is not in the corresponding set comes back
// as "missing" rather than as an API error -- the two failure modes the
// existence checks distinguish.
type fakeCFNValidationAPI struct {
	zones          []string
	images         []string
	instances      []string
	keyPairs       []string
	securityGroups map[string]string // id -> name
	subnets        []string
	volumes        []string
	vpcs           []string
	hostedZones    []string

	err error

	calls           map[string]int
	lastSGFilterVal []string
}

func (f *fakeCFNValidationAPI) record(api string) {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[api]++
}

// intersect returns the members of want that are in have.
func intersect(want, have []string) []string {
	set := map[string]bool{}
	for _, h := range have {
		set[h] = true
	}
	var out []string
	for _, w := range want {
		if set[w] {
			out = append(out, w)
		}
	}
	return out
}

func (f *fakeCFNValidationAPI) DescribeAvailabilityZones(_ context.Context, in *ec2.DescribeAvailabilityZonesInput, _ ...func(*ec2.Options)) (*ec2.DescribeAvailabilityZonesOutput, error) {
	f.record("DescribeAvailabilityZones")
	if f.err != nil {
		return nil, f.err
	}
	out := &ec2.DescribeAvailabilityZonesOutput{}
	for _, name := range intersect(in.ZoneNames, f.zones) {
		out.AvailabilityZones = append(out.AvailabilityZones, ec2types.AvailabilityZone{ZoneName: aws.String(name)})
	}
	return out, nil
}

func (f *fakeCFNValidationAPI) DescribeImages(_ context.Context, in *ec2.DescribeImagesInput, _ ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	f.record("DescribeImages")
	if f.err != nil {
		return nil, f.err
	}
	out := &ec2.DescribeImagesOutput{}
	for _, id := range intersect(in.ImageIds, f.images) {
		out.Images = append(out.Images, ec2types.Image{ImageId: aws.String(id)})
	}
	return out, nil
}

func (f *fakeCFNValidationAPI) DescribeInstances(_ context.Context, in *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	f.record("DescribeInstances")
	if f.err != nil {
		return nil, f.err
	}
	out := &ec2.DescribeInstancesOutput{}
	for _, id := range intersect(in.InstanceIds, f.instances) {
		out.Reservations = append(out.Reservations, ec2types.Reservation{
			Instances: []ec2types.Instance{{InstanceId: aws.String(id)}},
		})
	}
	return out, nil
}

func (f *fakeCFNValidationAPI) DescribeKeyPairs(_ context.Context, in *ec2.DescribeKeyPairsInput, _ ...func(*ec2.Options)) (*ec2.DescribeKeyPairsOutput, error) {
	f.record("DescribeKeyPairs")
	if f.err != nil {
		return nil, f.err
	}
	out := &ec2.DescribeKeyPairsOutput{}
	for _, name := range intersect(in.KeyNames, f.keyPairs) {
		out.KeyPairs = append(out.KeyPairs, ec2types.KeyPairInfo{KeyName: aws.String(name)})
	}
	return out, nil
}

func (f *fakeCFNValidationAPI) DescribeSecurityGroups(_ context.Context, in *ec2.DescribeSecurityGroupsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
	f.record("DescribeSecurityGroups")
	if f.err != nil {
		return nil, f.err
	}
	out := &ec2.DescribeSecurityGroupsOutput{}
	for _, id := range in.GroupIds {
		if name, ok := f.securityGroups[id]; ok {
			out.SecurityGroups = append(out.SecurityGroups, ec2types.SecurityGroup{
				GroupId: aws.String(id), GroupName: aws.String(name),
			})
		}
	}
	for _, filter := range in.Filters {
		if aws.ToString(filter.Name) != "group-name" {
			continue
		}
		f.lastSGFilterVal = filter.Values
		for id, name := range f.securityGroups {
			for _, wanted := range filter.Values {
				if wanted == name {
					out.SecurityGroups = append(out.SecurityGroups, ec2types.SecurityGroup{
						GroupId: aws.String(id), GroupName: aws.String(name),
					})
				}
			}
		}
	}
	return out, nil
}

func (f *fakeCFNValidationAPI) DescribeSubnets(_ context.Context, in *ec2.DescribeSubnetsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	f.record("DescribeSubnets")
	if f.err != nil {
		return nil, f.err
	}
	out := &ec2.DescribeSubnetsOutput{}
	for _, id := range intersect(in.SubnetIds, f.subnets) {
		out.Subnets = append(out.Subnets, ec2types.Subnet{SubnetId: aws.String(id)})
	}
	return out, nil
}

func (f *fakeCFNValidationAPI) DescribeVolumes(_ context.Context, in *ec2.DescribeVolumesInput, _ ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	f.record("DescribeVolumes")
	if f.err != nil {
		return nil, f.err
	}
	out := &ec2.DescribeVolumesOutput{}
	for _, id := range intersect(in.VolumeIds, f.volumes) {
		out.Volumes = append(out.Volumes, ec2types.Volume{VolumeId: aws.String(id)})
	}
	return out, nil
}

func (f *fakeCFNValidationAPI) DescribeVpcs(_ context.Context, in *ec2.DescribeVpcsInput, _ ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
	f.record("DescribeVpcs")
	if f.err != nil {
		return nil, f.err
	}
	out := &ec2.DescribeVpcsOutput{}
	for _, id := range intersect(in.VpcIds, f.vpcs) {
		out.Vpcs = append(out.Vpcs, ec2types.Vpc{VpcId: aws.String(id)})
	}
	return out, nil
}

func (f *fakeCFNValidationAPI) GetHostedZone(_ context.Context, in *route53.GetHostedZoneInput, _ ...func(*route53.Options)) (*route53.GetHostedZoneOutput, error) {
	f.record("GetHostedZone")
	if f.err != nil {
		return nil, f.err
	}
	id := aws.ToString(in.Id)
	for _, known := range f.hostedZones {
		if known == id {
			return &route53.GetHostedZoneOutput{HostedZone: &route53types.HostedZone{Id: aws.String(id)}}, nil
		}
	}
	return nil, errors.New("api error NoSuchHostedZone: No hosted zone found with ID: " + id)
}

// fakeValidator wires the fake into a cfnTypeValidator.
func fakeValidator(f *fakeCFNValidationAPI) *cfnTypeValidator {
	return &cfnTypeValidator{EC2: f, Route53: f}
}

// --- the value type table ---------------------------------------------------

// TestCFNValueTypeTables pins the exact set of CloudFormation-supplied
// parameter types, scalar and list. The list has no
// List<AWS::EC2::KeyPair::KeyName>, because CloudFormation has none.
func TestCFNValueTypeTables(t *testing.T) {
	t.Parallel()

	wantScalar := []string{
		"AWS::EC2::AvailabilityZone::Name",
		"AWS::EC2::Image::Id",
		"AWS::EC2::Instance::Id",
		"AWS::EC2::KeyPair::KeyName",
		"AWS::EC2::SecurityGroup::GroupName",
		"AWS::EC2::SecurityGroup::Id",
		"AWS::EC2::Subnet::Id",
		"AWS::EC2::VPC::Id",
		"AWS::EC2::Volume::Id",
		"AWS::Route53::HostedZone::Id",
		"String",
	}
	if got := sortedValueTypeNames(cfnScalarValueTypes); !equalStrings(got, wantScalar) {
		t.Errorf("scalar value types = %v, want %v", got, wantScalar)
	}

	wantList := []string{
		"CommaDelimitedList",
		"List<AWS::EC2::AvailabilityZone::Name>",
		"List<AWS::EC2::Image::Id>",
		"List<AWS::EC2::Instance::Id>",
		"List<AWS::EC2::SecurityGroup::GroupName>",
		"List<AWS::EC2::SecurityGroup::Id>",
		"List<AWS::EC2::Subnet::Id>",
		"List<AWS::EC2::VPC::Id>",
		"List<AWS::EC2::Volume::Id>",
		"List<AWS::Route53::HostedZone::Id>",
		"List<String>",
	}
	if got := sortedValueTypeNames(cfnListValueTypes); !equalStrings(got, wantList) {
		t.Errorf("list value types = %v, want %v", got, wantList)
	}

	if _, ok := cfnListValueTypes["List<AWS::EC2::KeyPair::KeyName>"]; ok {
		t.Error("List<AWS::EC2::KeyPair::KeyName> is in the list table, but CloudFormation has no such type")
	}
	if spec := cfnScalarValueTypes[cfnValueTypeString]; spec.HasExistenceCheck() || spec.Pattern != nil {
		t.Error("the plain String type must have neither a pattern nor an existence check")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestValidateCfnValueTypeSyntax covers the always-on syntactic check for
// every AWS-specific type, with no AWS clients at all.
func TestValidateCfnValueTypeSyntax(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		valueType string
		value     string
		valid     bool
	}{
		"ami 17 hex":               {cfnValueTypeImageID, "ami-0123456789abcdef0", true},
		"ami 8 hex":                {cfnValueTypeImageID, "ami-12345678", true},
		"ami wrong length":         {cfnValueTypeImageID, "ami-123", false},
		"ami uppercase hex":        {cfnValueTypeImageID, "ami-0123456789ABCDEF0", false},
		"ami without prefix":       {cfnValueTypeImageID, "0123456789abcdef0", false},
		"instance id":              {cfnValueTypeInstanceID, "i-0123456789abcdef0", true},
		"instance id is not a vpc": {cfnValueTypeVPCID, "i-0123456789abcdef0", false},
		"subnet id":                {cfnValueTypeSubnetID, "subnet-12345678", true},
		"security group id":        {cfnValueTypeSecurityGroupID, "sg-0123456789abcdef0", true},
		"volume id":                {cfnValueTypeVolumeID, "vol-12345678", true},
		"vpc id":                   {cfnValueTypeVPCID, "vpc-0123456789abcdef0", true},
		"az name":                  {cfnValueTypeAvailabilityZoneName, "us-east-1a", true},
		"az name gov":              {cfnValueTypeAvailabilityZoneName, "us-gov-west-1a", true},
		"az name local zone":       {cfnValueTypeAvailabilityZoneName, "us-east-1-bos-1a", true},
		"az name region only":      {cfnValueTypeAvailabilityZoneName, "us-east-1", false},
		"key pair name":            {cfnValueTypeKeyPairKeyName, "my key-pair.1", true},
		"key pair name empty":      {cfnValueTypeKeyPairKeyName, "", false},
		"security group name":      {cfnValueTypeSecurityGroupGroupName, "web-sg", true},
		"hosted zone id":           {cfnValueTypeHostedZoneID, "Z1D633PJN98FT9", true},
		"hosted zone id prefixed":  {cfnValueTypeHostedZoneID, "/hostedzone/Z1D633PJN98FT9", false},
		"hosted zone id lowercase": {cfnValueTypeHostedZoneID, "z1d633pjn98ft9", false},
		"plain String accepts anything": {cfnValueTypeString,
			"literally anything: {} [] !", true},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			spec := cfnScalarValueTypes[testCase.valueType]
			// checkExistence=false, so this is purely syntactic and needs no
			// clients at all.
			err := validateCfnValueType(context.Background(), spec, []string{testCase.value}, false, nil)
			if testCase.valid && err != nil {
				t.Fatalf("expected %q to be a valid %s, got: %s", testCase.value, testCase.valueType, err)
			}
			if !testCase.valid && err == nil {
				t.Fatalf("expected %q to be an invalid %s, got no error", testCase.value, testCase.valueType)
			}
		})
	}
}

// TestValidateCfnValueTypeExistence covers the AWS-side existence check for
// each type: the API it calls, the found case, the missing case, and the
// call-failed case, all through the fake.
func TestValidateCfnValueTypeExistence(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		valueType string
		present   string
		absent    string
		api       string
		fake      func() *fakeCFNValidationAPI
	}{
		"availability zone name": {
			cfnValueTypeAvailabilityZoneName, "us-east-1a", "us-east-1z", "DescribeAvailabilityZones",
			func() *fakeCFNValidationAPI { return &fakeCFNValidationAPI{zones: []string{"us-east-1a"}} },
		},
		"image id": {
			cfnValueTypeImageID, "ami-12345678", "ami-87654321", "DescribeImages",
			func() *fakeCFNValidationAPI { return &fakeCFNValidationAPI{images: []string{"ami-12345678"}} },
		},
		"instance id": {
			cfnValueTypeInstanceID, "i-12345678", "i-87654321", "DescribeInstances",
			func() *fakeCFNValidationAPI { return &fakeCFNValidationAPI{instances: []string{"i-12345678"}} },
		},
		"key pair name": {
			cfnValueTypeKeyPairKeyName, "my-key", "other-key", "DescribeKeyPairs",
			func() *fakeCFNValidationAPI { return &fakeCFNValidationAPI{keyPairs: []string{"my-key"}} },
		},
		"security group id": {
			cfnValueTypeSecurityGroupID, "sg-12345678", "sg-87654321", "DescribeSecurityGroups",
			func() *fakeCFNValidationAPI {
				return &fakeCFNValidationAPI{securityGroups: map[string]string{"sg-12345678": "web"}}
			},
		},
		"security group name": {
			cfnValueTypeSecurityGroupGroupName, "web", "database", "DescribeSecurityGroups",
			func() *fakeCFNValidationAPI {
				return &fakeCFNValidationAPI{securityGroups: map[string]string{"sg-12345678": "web"}}
			},
		},
		"subnet id": {
			cfnValueTypeSubnetID, "subnet-12345678", "subnet-87654321", "DescribeSubnets",
			func() *fakeCFNValidationAPI { return &fakeCFNValidationAPI{subnets: []string{"subnet-12345678"}} },
		},
		"volume id": {
			cfnValueTypeVolumeID, "vol-12345678", "vol-87654321", "DescribeVolumes",
			func() *fakeCFNValidationAPI { return &fakeCFNValidationAPI{volumes: []string{"vol-12345678"}} },
		},
		"vpc id": {
			cfnValueTypeVPCID, "vpc-12345678", "vpc-87654321", "DescribeVpcs",
			func() *fakeCFNValidationAPI { return &fakeCFNValidationAPI{vpcs: []string{"vpc-12345678"}} },
		},
		"hosted zone id": {
			cfnValueTypeHostedZoneID, "Z1D633PJN98FT9", "Z0000000000000", "GetHostedZone",
			func() *fakeCFNValidationAPI { return &fakeCFNValidationAPI{hostedZones: []string{"Z1D633PJN98FT9"}} },
		},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			spec := cfnScalarValueTypes[testCase.valueType]
			if !spec.HasExistenceCheck() {
				t.Fatalf("%s has no existence check", testCase.valueType)
			}
			if spec.IAMPermission == "" {
				t.Errorf("%s declares no IAM permission for its existence check", testCase.valueType)
			}

			// Present.
			f := testCase.fake()
			if err := validateCfnValueType(context.Background(), spec, []string{testCase.present}, true, fakeValidator(f)); err != nil {
				t.Fatalf("expected %q to exist, got: %s", testCase.present, err)
			}
			if f.calls[testCase.api] != 1 {
				t.Errorf("expected exactly one %s call, got %v", testCase.api, f.calls)
			}

			// Absent.
			f = testCase.fake()
			err := validateCfnValueType(context.Background(), spec, []string{testCase.absent}, true, fakeValidator(f))
			if err == nil {
				t.Fatalf("expected %q not to exist, got no error", testCase.absent)
			}
			if !strings.Contains(err.Error(), "does not exist in this account and region") {
				t.Errorf("expected a not-found diagnostic, got %q", err.Error())
			}
			if !strings.Contains(err.Error(), "validate = false") {
				t.Errorf("expected the diagnostic to name the escape hatch, got %q", err.Error())
			}

			// validate = false skips the call entirely.
			f = testCase.fake()
			if err := validateCfnValueType(context.Background(), spec, []string{testCase.absent}, false, fakeValidator(f)); err != nil {
				t.Fatalf("expected validate=false to skip the existence check, got: %s", err)
			}
			if len(f.calls) != 0 {
				t.Errorf("expected no API calls with validate=false, got %v", f.calls)
			}

			// A failing call names its permission rather than reporting
			// "does not exist".
			f = testCase.fake()
			f.err = errors.New("AccessDenied")
			err = validateCfnValueType(context.Background(), spec, []string{testCase.present}, true, fakeValidator(f))
			if err == nil {
				t.Fatal("expected a failing API call to be an error")
			}
			if !strings.Contains(err.Error(), spec.IAMPermission) {
				t.Errorf("expected the diagnostic to name %s, got %q", spec.IAMPermission, err.Error())
			}
		})
	}
}

// TestValidateCfnValueTypeListBatching checks that a list is validated in one
// batched call, with duplicates collapsed, and that every missing element is
// named.
func TestValidateCfnValueTypeListBatching(t *testing.T) {
	t.Parallel()

	spec := cfnListValueTypes["List<AWS::EC2::Subnet::Id>"]
	f := &fakeCFNValidationAPI{subnets: []string{"subnet-11111111", "subnet-22222222"}}

	values := []string{"subnet-11111111", "subnet-22222222", "subnet-11111111"}
	if err := validateCfnValueType(context.Background(), spec, values, true, fakeValidator(f)); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if f.calls["DescribeSubnets"] != 1 {
		t.Errorf("expected one batched DescribeSubnets call, got %v", f.calls)
	}

	f = &fakeCFNValidationAPI{subnets: []string{"subnet-11111111"}}
	err := validateCfnValueType(context.Background(), spec,
		[]string{"subnet-11111111", "subnet-22222222", "subnet-33333333"}, true, fakeValidator(f))
	if err == nil {
		t.Fatal("expected the two missing subnets to be an error")
	}
	for _, missing := range []string{"subnet-22222222", "subnet-33333333"} {
		if !strings.Contains(err.Error(), missing) {
			t.Errorf("expected the diagnostic to name %s, got %q", missing, err.Error())
		}
	}
	if strings.Contains(err.Error(), "subnet-11111111") {
		t.Errorf("expected the diagnostic not to name the subnet that does exist, got %q", err.Error())
	}
}

// TestValidateCfnValueTypeListSyntaxNamesTheIndex checks that a per-element
// syntax failure inside a list says which element failed.
func TestValidateCfnValueTypeListSyntaxNamesTheIndex(t *testing.T) {
	t.Parallel()

	spec := cfnListValueTypes["List<AWS::EC2::Subnet::Id>"]
	err := validateCfnValueType(context.Background(), spec,
		[]string{"subnet-11111111", "not-a-subnet"}, false, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "at index 1") {
		t.Errorf("expected the diagnostic to name the failing index, got %q", err.Error())
	}
}

// TestValidateCfnValueTypeNoClient checks that an existence check with no
// client configured reports a provider bug rather than panicking.
func TestValidateCfnValueTypeNoClient(t *testing.T) {
	t.Parallel()

	spec := cfnScalarValueTypes[cfnValueTypeImageID]
	err := validateCfnValueType(context.Background(), spec, []string{"ami-12345678"}, true, nil)
	if err == nil || !strings.Contains(err.Error(), "bug in the cfncompat provider") {
		t.Fatalf("expected a provider-bug diagnostic, got %v", err)
	}

	err = validateCfnValueType(context.Background(), spec, []string{"ami-12345678"}, true, &cfnTypeValidator{})
	if err == nil || !strings.Contains(err.Error(), "no EC2 client") {
		t.Fatalf("expected a missing-client diagnostic, got %v", err)
	}
}

// --- shared read helpers ----------------------------------------------------

func TestSSMParameterSelector(t *testing.T) {
	t.Parallel()

	if got := ssmParameterSelector("/a/b", types.Int64Null(), types.StringNull()); got != "/a/b" {
		t.Errorf("unpinned selector = %q, want %q", got, "/a/b")
	}
	if got := ssmParameterSelector("/a/b", types.Int64Value(3), types.StringNull()); got != "/a/b:3" {
		t.Errorf("version selector = %q, want %q", got, "/a/b:3")
	}
	if got := ssmParameterSelector("/a/b", types.Int64Null(), types.StringValue("prod")); got != "/a/b:prod" {
		t.Errorf("label selector = %q, want %q", got, "/a/b:prod")
	}
	// version wins if both somehow survive validation.
	if got := ssmParameterSelector("/a/b", types.Int64Value(3), types.StringValue("prod")); got != "/a/b:3" {
		t.Errorf("version+label selector = %q, want %q", got, "/a/b:3")
	}
}

func TestSSMListSplit(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		raw  string
		want []string
	}{
		"simple":                     {"a,b,c", []string{"a", "b", "c"}},
		"whitespace is trimmed":      {"a, b ,  c", []string{"a", "b", "c"}},
		"single element":             {"only", []string{"only"}},
		"empty string is one member": {"", []string{""}},
		"consecutive commas":         {"a,,c", []string{"a", "", "c"}},
		"trailing comma":             {"a,b,", []string{"a", "b", ""}},
		"tabs and newlines trimmed":  {"a,\tb\n,c", []string{"a", "b", "c"}},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := ssmListSplit(testCase.raw)
			if !equalStrings(got, testCase.want) {
				t.Fatalf("ssmListSplit(%q) = %v, want %v", testCase.raw, got, testCase.want)
			}
		})
	}
}

func TestCFNParameterConstraints(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		constraints   cfnParameterConstraints
		values        []string
		errorContains string
	}{
		"no constraints": {cfnParameterConstraints{}, []string{"anything"}, ""},
		"allowed pattern matches": {
			cfnParameterConstraints{AllowedPattern: `[a-z]+`}, []string{"abc"}, "",
		},
		"allowed pattern is anchored, not a substring match": {
			cfnParameterConstraints{AllowedPattern: `[a-z]+`}, []string{"abc1"}, "does not match `allowed_pattern`",
		},
		"invalid allowed pattern": {
			cfnParameterConstraints{AllowedPattern: `[a-z`}, []string{"abc"}, "not a valid regular expression",
		},
		"allowed values matches": {
			cfnParameterConstraints{AllowedValues: []string{"a", "b"}}, []string{"b"}, "",
		},
		"allowed values rejects": {
			cfnParameterConstraints{AllowedValues: []string{"a", "b"}}, []string{"c"}, "not one of `allowed_values`",
		},
		"applied per list element": {
			cfnParameterConstraints{AllowedValues: []string{"a", "b"}}, []string{"a", "z"}, "at index 1",
		},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := testCase.constraints.validate(testCase.values)
			if testCase.errorContains == "" {
				if err != nil {
					t.Fatalf("unexpected error: %s", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error containing %q, got none", testCase.errorContains)
			}
			if !strings.Contains(err.Error(), testCase.errorContains) {
				t.Fatalf("expected error to contain %q, got %q", testCase.errorContains, err.Error())
			}
		})
	}
}
