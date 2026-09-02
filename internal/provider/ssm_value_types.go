// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
)

// This file holds the CloudFormation-supplied parameter type table shared by
// the three SSM parameter data sources. It is the provider-side counterpart of
// the inner type of `AWS::SSM::Parameter::Value<...>`: CloudFormation resolves
// the Parameter Store value and then validates it as the declared type, and
// these data sources do the same.
//
// Types are taken verbatim from "CloudFormation-supplied parameter types"
// (cloudformation-supplied-parameter-types.html): ten AWS-specific scalar
// types, and a List<> form of nine of them -- CloudFormation supports no
// List<AWS::EC2::KeyPair::KeyName>.

// CFN value type names. Declared as constants so the data sources, the
// validators and the tests all name the same strings.
const (
	cfnValueTypeString             = "String"
	cfnValueTypeListString         = "List<String>"
	cfnValueTypeCommaDelimitedList = "CommaDelimitedList"

	cfnValueTypeAvailabilityZoneName   = "AWS::EC2::AvailabilityZone::Name"
	cfnValueTypeImageID                = "AWS::EC2::Image::Id"
	cfnValueTypeInstanceID             = "AWS::EC2::Instance::Id"
	cfnValueTypeKeyPairKeyName         = "AWS::EC2::KeyPair::KeyName"
	cfnValueTypeSecurityGroupGroupName = "AWS::EC2::SecurityGroup::GroupName"
	cfnValueTypeSecurityGroupID        = "AWS::EC2::SecurityGroup::Id"
	cfnValueTypeSubnetID               = "AWS::EC2::Subnet::Id"
	cfnValueTypeVolumeID               = "AWS::EC2::Volume::Id"
	cfnValueTypeVPCID                  = "AWS::EC2::VPC::Id"
	cfnValueTypeHostedZoneID           = "AWS::Route53::HostedZone::Id"
)

// ec2ResourceIDPattern is the shared shape of an EC2 resource identifier: the
// resource's prefix plus either the legacy 8 or the current 17 hexadecimal
// characters.
func ec2ResourceIDPattern(prefix string) *regexp.Regexp {
	return regexp.MustCompile(`^` + prefix + `-([0-9a-f]{8}|[0-9a-f]{17})$`)
}

// cfnValueTypeSpec describes one CloudFormation-supplied parameter type: the
// syntactic shape every value must have, and -- for the AWS-specific types --
// the API call CloudFormation makes to assert the value actually exists in the
// account and region.
type cfnValueTypeSpec struct {
	// Name is the CloudFormation type name, e.g. "AWS::EC2::Image::Id".
	Name string
	// Pattern, when non-nil, every value must match. A nil Pattern means the
	// type places no syntactic constraint on the value (plain String).
	Pattern *regexp.Regexp
	// PatternDescription describes Pattern in a diagnostic, e.g.
	// "ami- followed by 8 or 17 hexadecimal characters".
	PatternDescription string
	// IAMPermission is the IAM action the existence check needs, empty when
	// the type has no existence check.
	IAMPermission string
	// exists asserts that every value in values exists. It is nil for the
	// plain String type, which CloudFormation does not validate either.
	exists func(ctx context.Context, v *cfnTypeValidator, values []string) error
}

// HasExistenceCheck reports whether this type has an AWS-side existence check,
// i.e. whether `validate = true` costs an API call.
func (s cfnValueTypeSpec) HasExistenceCheck() bool { return s.exists != nil }

// cfnScalarValueTypes is every inner type valid inside
// AWS::SSM::Parameter::Value<...> for a scalar (String-backed) parameter.
var cfnScalarValueTypes = buildScalarValueTypes()

func buildScalarValueTypes() map[string]cfnValueTypeSpec {
	specs := []cfnValueTypeSpec{
		{
			Name: cfnValueTypeString,
		},
		{
			Name: cfnValueTypeAvailabilityZoneName,
			// us-east-1a, eu-west-1c, us-gov-west-1a, and the longer
			// Local Zone names such as us-east-1-bos-1a.
			Pattern:            regexp.MustCompile(`^[a-z]{2}(-[a-z0-9]+)+-[0-9]+[a-z]$`),
			PatternDescription: "an Availability Zone name such as \"us-east-1a\"",
			IAMPermission:      "ec2:DescribeAvailabilityZones",
			exists:             existsAvailabilityZoneNames,
		},
		{
			Name:               cfnValueTypeImageID,
			Pattern:            ec2ResourceIDPattern("ami"),
			PatternDescription: "\"ami-\" followed by 8 or 17 hexadecimal characters",
			IAMPermission:      "ec2:DescribeImages",
			exists:             existsImageIDs,
		},
		{
			Name:               cfnValueTypeInstanceID,
			Pattern:            ec2ResourceIDPattern("i"),
			PatternDescription: "\"i-\" followed by 8 or 17 hexadecimal characters",
			IAMPermission:      "ec2:DescribeInstances",
			exists:             existsInstanceIDs,
		},
		{
			Name: cfnValueTypeKeyPairKeyName,
			// EC2 key pair names are 1-255 ASCII characters.
			Pattern:            regexp.MustCompile(`^[\x20-\x7E]{1,255}$`),
			PatternDescription: "1 to 255 printable ASCII characters",
			IAMPermission:      "ec2:DescribeKeyPairs",
			exists:             existsKeyPairNames,
		},
		{
			Name:               cfnValueTypeSecurityGroupGroupName,
			Pattern:            regexp.MustCompile(`^[a-zA-Z0-9 ._\-:/()#,@\[\]+=&;{}!$*]{1,255}$`),
			PatternDescription: "1 to 255 characters from the EC2 security group name character set",
			IAMPermission:      "ec2:DescribeSecurityGroups",
			exists:             existsSecurityGroupNames,
		},
		{
			Name:               cfnValueTypeSecurityGroupID,
			Pattern:            ec2ResourceIDPattern("sg"),
			PatternDescription: "\"sg-\" followed by 8 or 17 hexadecimal characters",
			IAMPermission:      "ec2:DescribeSecurityGroups",
			exists:             existsSecurityGroupIDs,
		},
		{
			Name:               cfnValueTypeSubnetID,
			Pattern:            ec2ResourceIDPattern("subnet"),
			PatternDescription: "\"subnet-\" followed by 8 or 17 hexadecimal characters",
			IAMPermission:      "ec2:DescribeSubnets",
			exists:             existsSubnetIDs,
		},
		{
			Name:               cfnValueTypeVolumeID,
			Pattern:            ec2ResourceIDPattern("vol"),
			PatternDescription: "\"vol-\" followed by 8 or 17 hexadecimal characters",
			IAMPermission:      "ec2:DescribeVolumes",
			exists:             existsVolumeIDs,
		},
		{
			Name:               cfnValueTypeVPCID,
			Pattern:            ec2ResourceIDPattern("vpc"),
			PatternDescription: "\"vpc-\" followed by 8 or 17 hexadecimal characters",
			IAMPermission:      "ec2:DescribeVpcs",
			exists:             existsVPCIDs,
		},
		{
			Name: cfnValueTypeHostedZoneID,
			// Route 53 hosted zone ids are uppercase alphanumerics; the
			// "/hostedzone/" prefix some APIs return is not part of the id
			// CloudFormation validates.
			Pattern:            regexp.MustCompile(`^[A-Z0-9]{1,32}$`),
			PatternDescription: "1 to 32 uppercase alphanumeric characters (a Route 53 hosted zone ID, without the \"/hostedzone/\" prefix)",
			IAMPermission:      "route53:GetHostedZone",
			exists:             existsHostedZoneIDs,
		},
	}

	out := make(map[string]cfnValueTypeSpec, len(specs))
	for _, s := range specs {
		out[s.Name] = s
	}
	return out
}

// cfnListValueTypes is every inner type valid inside
// AWS::SSM::Parameter::Value<...> for a list (StringList-backed) parameter:
// List<String>, CommaDelimitedList, and List<T> for every AWS-specific scalar
// type except AWS::EC2::KeyPair::KeyName, which CloudFormation has no list
// form of.
var cfnListValueTypes = buildListValueTypes()

func buildListValueTypes() map[string]cfnValueTypeSpec {
	out := map[string]cfnValueTypeSpec{
		cfnValueTypeListString:         {Name: cfnValueTypeListString},
		cfnValueTypeCommaDelimitedList: {Name: cfnValueTypeCommaDelimitedList},
	}
	for name, spec := range cfnScalarValueTypes {
		if name == cfnValueTypeString || name == cfnValueTypeKeyPairKeyName {
			// CloudFormation has no List<String> via the scalar table (it is
			// spelled List<String> above) and no
			// List<AWS::EC2::KeyPair::KeyName> at all.
			continue
		}
		listName := "List<" + name + ">"
		listSpec := spec
		listSpec.Name = listName
		out[listName] = listSpec
	}
	return out
}

// sortedValueTypeNames returns the keys of a value-type table in a stable
// order, for diagnostics and documentation.
func sortedValueTypeNames(table map[string]cfnValueTypeSpec) []string {
	names := make([]string, 0, len(table))
	for name := range table {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// cfnEC2ValidationAPI is the subset of the EC2 API used by the AWS-specific
// value-type existence checks -- the same Describe* calls CloudFormation makes
// when it validates an AWS::SSM::Parameter::Value<AWS::EC2::...> value.
// Implemented by *ec2.Client; faked in tests so the checks run without AWS.
type cfnEC2ValidationAPI interface {
	DescribeAvailabilityZones(ctx context.Context, params *ec2.DescribeAvailabilityZonesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeAvailabilityZonesOutput, error)
	DescribeImages(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error)
	DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	DescribeKeyPairs(ctx context.Context, params *ec2.DescribeKeyPairsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeKeyPairsOutput, error)
	DescribeSecurityGroups(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error)
	DescribeSubnets(ctx context.Context, params *ec2.DescribeSubnetsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error)
	DescribeVolumes(ctx context.Context, params *ec2.DescribeVolumesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error)
	DescribeVpcs(ctx context.Context, params *ec2.DescribeVpcsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error)
}

// cfnRoute53ValidationAPI is the subset of the Route 53 API used by the
// AWS::Route53::HostedZone::Id existence check.
type cfnRoute53ValidationAPI interface {
	GetHostedZone(ctx context.Context, params *route53.GetHostedZoneInput, optFns ...func(*route53.Options)) (*route53.GetHostedZoneOutput, error)
}

// cfnTypeValidator carries the clients the AWS-specific existence checks need.
// A nil field means the corresponding check cannot run and reports so rather
// than panicking.
type cfnTypeValidator struct {
	EC2     cfnEC2ValidationAPI
	Route53 cfnRoute53ValidationAPI
}

// validateCfnValueType applies the CloudFormation-supplied parameter type to
// resolved values: always the syntactic check, and -- when checkExistence is
// true and the type has one -- the AWS-side existence check.
//
// values is a single-element slice for a scalar type and the split elements
// for a list type, so both data sources share one implementation and the
// existence check is a single batched API call either way.
func validateCfnValueType(ctx context.Context, spec cfnValueTypeSpec, values []string, checkExistence bool, v *cfnTypeValidator) error {
	if spec.Pattern != nil {
		for i, value := range values {
			if spec.Pattern.MatchString(value) {
				continue
			}
			return fmt.Errorf(
				"the resolved value %s is not a valid %s: CloudFormation requires %s",
				describeValueAt(values, i), spec.Name, spec.PatternDescription,
			)
		}
	}

	if !checkExistence || spec.exists == nil {
		return nil
	}
	if len(values) == 0 {
		return nil
	}
	if v == nil {
		return fmt.Errorf(
			"cannot check that the resolved value exists as a %s: no AWS clients were configured "+
				"(this is a bug in the cfncompat provider; please report it)", spec.Name)
	}
	return spec.exists(ctx, v, dedupe(values))
}

// describeValueAt renders a value for a diagnostic, naming its list position
// when it came from a list.
func describeValueAt(values []string, i int) string {
	if len(values) == 1 {
		return fmt.Sprintf("%q", values[i])
	}
	return fmt.Sprintf("%q at index %d", values[i], i)
}

// dedupe returns values with duplicates removed, order preserved: the
// existence checks pass ids straight to an AWS API, and a repeated id is at
// best wasted request payload and at worst an API error.
func dedupe(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// missingValuesError builds the diagnostic for values that the AWS API did not
// return. It is the shape CloudFormation's own validation error has: name the
// type, name the values, and say where they were looked for.
func missingValuesError(typeName string, missing []string) error {
	sort.Strings(missing)
	return fmt.Errorf(
		"%s %s %s not exist in this account and region; CloudFormation rejects a "+
			"%s parameter whose value does not resolve to a real resource. Set `validate = false` to "+
			"skip this existence check and keep only the syntactic one",
		typeName, quoteAll(missing), plural(len(missing), "does", "do"), typeName,
	)
}

// describeCallError wraps an AWS API failure from an existence check, naming
// the permission the check needs so a denied call is self-explanatory.
func describeCallError(typeName, api, permission string, err error) error {
	return fmt.Errorf(
		"calling %s to check that the resolved %s value exists (requires the %s permission; "+
			"set `validate = false` to skip the existence check): %w",
		api, typeName, permission, err)
}

func quoteAll(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	return strings.Join(quoted, ", ")
}

func plural(n int, singular, pluralForm string) string {
	if n == 1 {
		return singular
	}
	return pluralForm
}

// missingFrom returns the values that are not in found.
func missingFrom(values []string, found map[string]bool) []string {
	var missing []string
	for _, v := range values {
		if !found[v] {
			missing = append(missing, v)
		}
	}
	return missing
}

// ec2ExistenceCheck builds a cfnValueTypeSpec.exists from the one thing each
// AWS-specific EC2 type does differently: which Describe* call answers "which
// of these do you know about", and which field of the response carries the
// identifier that was asked for. Everything around that -- the missing client
// guard, the error wrapping, and comparing what came back against what was
// asked for -- is identical for every type.
func ec2ExistenceCheck(
	typeName, api, permission string,
	lookup func(ctx context.Context, client cfnEC2ValidationAPI, values []string) ([]string, error),
) func(ctx context.Context, v *cfnTypeValidator, values []string) error {
	return func(ctx context.Context, v *cfnTypeValidator, values []string) error {
		if v.EC2 == nil {
			return errNoEC2Client(typeName)
		}
		known, err := lookup(ctx, v.EC2, values)
		if err != nil {
			return describeCallError(typeName, api, permission, err)
		}
		found := make(map[string]bool, len(known))
		for _, k := range known {
			found[k] = true
		}
		if missing := missingFrom(values, found); len(missing) > 0 {
			return missingValuesError(typeName, missing)
		}
		return nil
	}
}

// idsOf maps an AWS API response slice to the identifiers it reports.
func idsOf[T any](items []T, id func(T) string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, id(item))
	}
	return out
}

var existsAvailabilityZoneNames = ec2ExistenceCheck(
	cfnValueTypeAvailabilityZoneName, "EC2 DescribeAvailabilityZones", "ec2:DescribeAvailabilityZones",
	func(ctx context.Context, client cfnEC2ValidationAPI, values []string) ([]string, error) {
		out, err := client.DescribeAvailabilityZones(ctx, &ec2.DescribeAvailabilityZonesInput{ZoneNames: values})
		if err != nil || out == nil {
			return nil, err
		}
		return idsOf(out.AvailabilityZones, func(az ec2types.AvailabilityZone) string { return aws.ToString(az.ZoneName) }), nil
	})

var existsImageIDs = ec2ExistenceCheck(
	cfnValueTypeImageID, "EC2 DescribeImages", "ec2:DescribeImages",
	func(ctx context.Context, client cfnEC2ValidationAPI, values []string) ([]string, error) {
		out, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{ImageIds: values})
		if err != nil || out == nil {
			return nil, err
		}
		return idsOf(out.Images, func(img ec2types.Image) string { return aws.ToString(img.ImageId) }), nil
	})

var existsInstanceIDs = ec2ExistenceCheck(
	cfnValueTypeInstanceID, "EC2 DescribeInstances", "ec2:DescribeInstances",
	func(ctx context.Context, client cfnEC2ValidationAPI, values []string) ([]string, error) {
		out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: values})
		if err != nil || out == nil {
			return nil, err
		}
		// DescribeInstances nests instances one level deeper than every other
		// Describe* call, under the reservation that launched them.
		var ids []string
		for _, reservation := range out.Reservations {
			ids = append(ids, idsOf(reservation.Instances, func(i ec2types.Instance) string { return aws.ToString(i.InstanceId) })...)
		}
		return ids, nil
	})

var existsKeyPairNames = ec2ExistenceCheck(
	cfnValueTypeKeyPairKeyName, "EC2 DescribeKeyPairs", "ec2:DescribeKeyPairs",
	func(ctx context.Context, client cfnEC2ValidationAPI, values []string) ([]string, error) {
		out, err := client.DescribeKeyPairs(ctx, &ec2.DescribeKeyPairsInput{KeyNames: values})
		if err != nil || out == nil {
			return nil, err
		}
		return idsOf(out.KeyPairs, func(kp ec2types.KeyPairInfo) string { return aws.ToString(kp.KeyName) }), nil
	})

var existsSecurityGroupIDs = ec2ExistenceCheck(
	cfnValueTypeSecurityGroupID, "EC2 DescribeSecurityGroups", "ec2:DescribeSecurityGroups",
	func(ctx context.Context, client cfnEC2ValidationAPI, values []string) ([]string, error) {
		out, err := client.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{GroupIds: values})
		if err != nil || out == nil {
			return nil, err
		}
		return idsOf(out.SecurityGroups, func(sg ec2types.SecurityGroup) string { return aws.ToString(sg.GroupId) }), nil
	})

var existsSecurityGroupNames = ec2ExistenceCheck(
	cfnValueTypeSecurityGroupGroupName, "EC2 DescribeSecurityGroups", "ec2:DescribeSecurityGroups",
	func(ctx context.Context, client cfnEC2ValidationAPI, values []string) ([]string, error) {
		// Security group names are not unique across VPCs, so they are looked
		// up through the group-name filter rather than the GroupNames field
		// (which is EC2-Classic/default-VPC only).
		out, err := client.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
			Filters: []ec2types.Filter{{Name: aws.String("group-name"), Values: values}},
		})
		if err != nil || out == nil {
			return nil, err
		}
		return idsOf(out.SecurityGroups, func(sg ec2types.SecurityGroup) string { return aws.ToString(sg.GroupName) }), nil
	})

var existsSubnetIDs = ec2ExistenceCheck(
	cfnValueTypeSubnetID, "EC2 DescribeSubnets", "ec2:DescribeSubnets",
	func(ctx context.Context, client cfnEC2ValidationAPI, values []string) ([]string, error) {
		out, err := client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{SubnetIds: values})
		if err != nil || out == nil {
			return nil, err
		}
		return idsOf(out.Subnets, func(s ec2types.Subnet) string { return aws.ToString(s.SubnetId) }), nil
	})

var existsVolumeIDs = ec2ExistenceCheck(
	cfnValueTypeVolumeID, "EC2 DescribeVolumes", "ec2:DescribeVolumes",
	func(ctx context.Context, client cfnEC2ValidationAPI, values []string) ([]string, error) {
		out, err := client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{VolumeIds: values})
		if err != nil || out == nil {
			return nil, err
		}
		return idsOf(out.Volumes, func(v ec2types.Volume) string { return aws.ToString(v.VolumeId) }), nil
	})

var existsVPCIDs = ec2ExistenceCheck(
	cfnValueTypeVPCID, "EC2 DescribeVpcs", "ec2:DescribeVpcs",
	func(ctx context.Context, client cfnEC2ValidationAPI, values []string) ([]string, error) {
		out, err := client.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{VpcIds: values})
		if err != nil || out == nil {
			return nil, err
		}
		return idsOf(out.Vpcs, func(v ec2types.Vpc) string { return aws.ToString(v.VpcId) }), nil
	})

// existsHostedZoneIDs checks each hosted zone id with its own GetHostedZone
// call: Route 53 has no batch "describe these zones" API, so unlike the EC2
// checks this one costs one request per distinct value.
func existsHostedZoneIDs(ctx context.Context, v *cfnTypeValidator, values []string) error {
	if v.Route53 == nil {
		return fmt.Errorf(
			"cannot check that the resolved value exists as a %s: no Route 53 client was configured "+
				"(this is a bug in the cfncompat provider; please report it)", cfnValueTypeHostedZoneID)
	}
	var missing []string
	for _, id := range values {
		out, err := v.Route53.GetHostedZone(ctx, &route53.GetHostedZoneInput{Id: aws.String(id)})
		if err != nil {
			if isRoute53NoSuchHostedZone(err) {
				missing = append(missing, id)
				continue
			}
			return describeCallError(cfnValueTypeHostedZoneID, "Route 53 GetHostedZone", "route53:GetHostedZone", err)
		}
		if out == nil || out.HostedZone == nil {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return missingValuesError(cfnValueTypeHostedZoneID, missing)
	}
	return nil
}

// isRoute53NoSuchHostedZone reports whether err is Route 53's
// NoSuchHostedZone, i.e. "the zone does not exist" rather than "the call
// failed". Matched on the error string rather than errors.As so that a fake
// client in tests can report it without constructing SDK internals.
func isRoute53NoSuchHostedZone(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "NoSuchHostedZone")
}

func errNoEC2Client(typeName string) error {
	return fmt.Errorf(
		"cannot check that the resolved value exists as a %s: no EC2 client was configured "+
			"(this is a bug in the cfncompat provider; please report it)", typeName)
}
