# RFC 007: Dynamic-Reference Polyfill (SSM Parameter Store, Secrets Manager)

|  |  |
|---|---|
| **Status:** | Implemented |
| **Companion:** | `002-architecture.md` (§I2 `IIntrinsicResolver`), `004-intrinsic-function-polyfill.md` (function naming/exclusion rules), `006-pseudo-parameter-polyfill.md` (data-source precedent and the "accessor, never section rendering" ownership split) |
| **Origin:** | `cdktn-awscc/docs/bridge-gap-categories.md:148-156` — "CFN dynamic parameter types are a CFN-engine feature resolved at deploy time — exactly cfncompat's vehicle: spike a `cfncompat_ssm_parameter_value`-style data-source polyfill" |
| **Supporting research:** | [`dynamic-ssm/design-ssm-cfn-mechanics.md`](dynamic-ssm/design-ssm-cfn-mechanics.md) (CloudFormation semantics, aws-cdk consumption inventory, candidate designs), [`dynamic-ssm/design-ssm-tf-typing.md`](dynamic-ssm/design-ssm-tf-typing.md) (plugin-framework typing findings), [`dynamic-ssm/live-test-results.md`](dynamic-ssm/live-test-results.md) (**36 real CloudFormation stacks**, us-east-1; every `T<n>` citation below refers to it) |

## 1. Decision

Four **data sources** and two pure **provider functions** fill the CloudFormation
dynamic-reference gap, so a CDK Terrain synthesis targeting `hashicorp/awscc` + `cfncompat`
needs no `hashicorp/aws` for `AWS::SSM::Parameter::Value<...>` or for any of the three
`{{resolve:...}}` services:

| Surface | CloudFormation equivalent |
|---|---|
| `data "cfncompat_ssm_parameter_value"` | whole-value `{{resolve:ssm:...}}` (`value_type` unset) **or** `AWS::SSM::Parameter::Value<String>` / `<AWS-specific type>` (`value_type` set) — §4.6 |
| `data "cfncompat_ssm_parameter_list_value"` | `AWS::SSM::Parameter::Value<List<String>>`, `<CommaDelimitedList>`, `<List<AWS-specific type>>`; `Fn::Split(",", {{resolve:ssm:...}})` |
| `data "cfncompat_ssm_secure_parameter_value"` | `{{resolve:ssm-secure:parameter-name:version}}` |
| `data "cfncompat_secretsmanager_secret_value"` | `{{resolve:secretsmanager:secret-id:SecretString:json-key:version-stage:version-id}}` |
| `provider::cfncompat::parse_dynamic_reference` | splitting a `{{resolve:...}}` string whose text is only known at plan time |
| `provider::cfncompat::is_dynamic_reference` | testing whether a plan-time string is such a reference |

Explicitly **not** in scope: `StringParameter.valueFromLookup` (a synth-time CDK context
provider writing `cdk.context.json`, never a deploy-time value and so never cfncompat's);
`AWS::SSM::Parameter::Name` (an existence assertion no aws-cdk-lib L2 uses — §9 Q2);
substring expansion of a reference embedded in a larger string (§5); and any resource that
*writes* Parameter Store or Secrets Manager (awscc covers those).

## 2. The three CloudFormation mechanisms

### 2.1 `AWS::SSM::Parameter::Value<T>` template parameter types

A `Parameters` entry whose `Type` is an SSM type takes a Parameter Store **key** as its value
and `Ref` yields the **resolved value**. Ten AWS-specific inner types exist, plus `String`,
`List<String>` and `CommaDelimitedList`; `List<T>` exists for nine of the ten (there is no
`List<AWS::EC2::KeyPair::KeyName>`). `SecureString` is not a legal parameter type at all.

Re-resolution is **unconditional**: a `UsePreviousValue=true` update that changes nothing else
still re-resolves (T5a), and so does a change set whose `Changes` list is empty — its
`Parameters[].ResolvedValue` already carries the fresh value (T5b). "Use existing value" keeps
the key, not the value.

Type checking is **strict on the declared Systems Manager type, and blind to content** (T4b–d):
a `String` parameter whose value is `"x,y,z"` cannot satisfy `<CommaDelimitedList>` or
`<List<String>>`, and a `StringList` cannot satisfy `<String>` — both are rejected
synchronously with *"Types for SSM parameters [...] defined in CFN template and SSM are
incompatible"*. A `SecureString` behind any SSM parameter type is rejected synchronously with
*"Parameters [...] referenced by template have types not supported by CloudFormation"* (T3e).

Value validation for the AWS-specific inner types is a real **existence** check, not a regex:
a syntactically perfect but non-existent `ami-0123456789abcdef0` fails exactly like the garbage
string `not-an-ami`, both as *"parameter value X for parameter name P does not exist"* (T3a/c),
per element for a list type (T4f). It happens at **stack-level pre-resource validation**, before
any resource event — which maps cleanly onto Terraform's plan time. `AWS::SSM::Parameter::Name`
validates existence too (T3f), though it is out of scope here.

`name:version` works behind a typed parameter, including in its `Default` (T1a/b/e); a
non-existent version is a clean synchronous `ValidationError` (T1d). A `name:label` value
produces an opaque, reproducible `InternalFailure` (T1c) — labels are simply unsupported.

`List<...>` resolution **trims** the whitespace around each element: a `StringList` stored as
`"a,b, c ,d"` resolves to `a,b,c,d` (T4a); the trimming happens in CloudFormation, not in
Systems Manager. Contrast the dynamic-reference path, §2.2.

The resolved plaintext is visible in `DescribeStacks` as `Parameter.ResolvedValue` — which is
why `SecureString` is excluded, and why this RFC treats non-secret resolved values as
non-sensitive (§4.1).

### 2.2 `{{resolve:ssm:...}}` and `{{resolve:ssm-secure:...}}`

Pattern `{{resolve:ssm:[a-zA-Z0-9_.\-/]+(:\d+)?}}`, identically for `ssm-secure`. Both take an
optional numeric version; **labels are not supported**, nor are cross-account parameters, and
`ssm-secure` additionally supports no public parameters and is legal only in eleven
allow-listed password-shaped resource properties.

Re-resolution is **unconditional for both** (T6a–c): an update with a byte-identical template,
an update that only adds an unrelated resource, and an update that changes a different property
of the same resource all re-resolve the reference. The documentation's advice to "also perform
a stack update" to pick up a new value is therefore satisfied by *any* update — the earlier
mechanics study read it as "CloudFormation does not re-resolve", which the live tests
disprove. Terraform's read-every-plan behaviour is consequently **faithful** for every SSM path
(§7).

Labels are rejected cleanly here, with *"Incorrect format is used in the following SSM
reference"* (T6d for `ssm`, T8e for `ssm-secure`). A `StringList` read through
`{{resolve:ssm:...}}` returns the stored string **raw and untrimmed** — `"a,b, c ,d"` verbatim
(T6e) — the opposite of the typed `List<...>` path's trimming. `{{resolve:ssm:...}}` against a
`SecureString` is rejected with *"Non-secure ssm prefix was used for secure parameter X"*
(T8d). An unversioned reference in a `Parameters` `Default` is rejected (*"should not contain
ssm versionless resolver"*, T7a); a versioned one is fine (T7b). Mid-string references,
several references in one string, and references nested in `Fn::Sub` all interpolate (T7c–e).
Public `/aws/service/...` parameters behave identically to private ones (T10). An `ssm-secure`
reference in a non-allow-listed property fails with *"SSM Secure reference is not supported in:
[AWS::SSM::Parameter/Properties/Value]"* (T8a/b) — synchronously when a version is present,
asynchronously when not; the allow-list itself stays documentation-sourced.

`ssm-secure` never stores the value: CloudFormation stores the literal reference string,
resolves it during each operation, returns it from no API, and compares *the reference text*
in change sets. Terraform cannot reproduce this (§4.3).

### 2.3 `{{resolve:secretsmanager:...}}`

Pattern `{{resolve:secretsmanager:secret-id:secret-string:json-key:version-stage:version-id}}`.
`secret-string` has exactly one legal value, `SecretString`. `json-key`, `version-stage` and
`version-id` may not contain a colon; the `secret-id` may, because it may be a full ARN.
Omitting both version selectors means `AWSCURRENT`, which is the documented best practice
because it is what lets rotation work without a template change. Unlike `ssm-secure`, it is
legal in **every** resource property, and cross-account works with a full ARN (T9).

**Re-resolution is the odd one out.** A `secretsmanager` reference is re-resolved *only* when
CloudFormation is independently updating the resource that holds it: after a
`put-secret-value` rotated `AWSCURRENT`, an update that merely added an unrelated resource left
the consumer serving the old value — CloudFormation emitted no `UPDATE_*` event for it at all —
and only a genuine property change on that resource picked the new secret up (T9). This is the
opposite of `ssm`/`ssm-secure`, and it is the one place where Terraform's read-every-plan
behaviour genuinely diverges (§7).

Errors are **resource-level** `CREATE_FAILED`, not stack-level: *"Could not find a value
associated with JSONKey in SecretString"* and *"Could not parse SecretString JSON"* (T9). Both
messages are reused verbatim in this provider's diagnostics. A dynamic reference used directly
as an `Outputs` `Value` is **not resolved at all** — the literal placeholder string comes back
(T9) — so a backend must never route one through an output.

## 3. Mapping table: CloudFormation construct → cfncompat surface

| CloudFormation / aws-cdk-lib construct | Emitted by the backend as |
|---|---|
| `AWS::SSM::Parameter::Value<String>` used as an SSM-read vehicle (`valueForStringParameter`, `MachineImage.latestAmazonLinux*`, the EKS/ECS AMI paths) | **no `TerraformVariable`** — the `CfnParameter` is elided and a `cfncompat_ssm_parameter_value` is emitted keyed on the parameter's `Default`, **with `value_type = "String"`**; `Ref` wires to `.value` |
| `AWS::SSM::Parameter::Value<AWS-specific type>` | same, with `value_type` set to the inner type |
| `AWS::SSM::Parameter::Value<List<...>>` / `<CommaDelimitedList>` | `cfncompat_ssm_parameter_list_value`; `Ref` wires to `.values` |
| A genuinely user-authored SSM-typed `CfnParameter` (the *name* is the input) | `TerraformVariable` (the name) **+** the data source keyed on it |
| Whole-value `{{resolve:ssm:name[:version]}}` | `cfncompat_ssm_parameter_value` with **`value_type` left unset**, `version` from the reference's version segment. Leaving it unset is load-bearing: it selects dynamic-reference semantics, which accept a `StringList` and return the raw untrimmed string, exactly as CloudFormation does (T6e) |
| `{{resolve:ssm:...}}` **inside** a larger string | the backend splits the string, emits one data source per distinct reference, and rebuilds with HCL interpolation (§5) |
| `{{resolve:ssm-secure:name[:version]}}` / `SecretValue.ssmSecure` | `cfncompat_ssm_secure_parameter_value` |
| `{{resolve:secretsmanager:...}}` / `SecretValue.secretsManager` | `cfncompat_secretsmanager_secret_value` |
| A reference string not known until plan/apply | `provider::cfncompat::parse_dynamic_reference(expr)` feeding the data source's arguments (§5) |
| A `{{resolve:...}}` reference in an `Outputs` `Value` | **nothing** — CloudFormation does not resolve it there either; the literal string is returned (T9) |
| `StringParameter.valueFromLookup` | **nothing** — a synth-time CDK context provider; it resolves before Terraform ever runs |
| `ResolveSsmParameterAtLaunchImage` (bare `resolve:ssm:...`, no braces) | **nothing** — EC2, not CloudFormation, resolves it at instance launch; pass the string through |

The list/scalar discrimination is CDK's own rule (`core/lib/cfn-parameter.ts:355-371`): a type
is a list iff it contains `List<` or `CommaDelimitedList`. The backend already knows the CFN
type, so it always knows which data source to emit — **and whether to set `value_type` at
all**: it is set on the `CfnParameter` path and left unset on the `{{resolve:ssm}}` path.

Two things the backend must **not** do. It must never emit `label` (CloudFormation supports
Systems Manager labels nowhere: T1c, T6d, T8e). And it must never map a `CfnParameter`'s
`AllowedPattern`/`AllowedValues`/`MinLength`/`MaxLength` onto the data sources' constraint
arguments — in CloudFormation those constrain the literal parameter **name**, not the resolved
value (T2, §6.1), and the name is a literal the backend holds at synth time and can check
itself.

## 4. Why separate data sources per CloudFormation type (Design A)

The research proposed a single data source with `value` + `values` + `insecure_value`
companions, matching `hashicorp/aws`. That was rejected. Six reasons, in decreasing order of
weight:

**4.1 Sensitivity is static, so it must be a property of the data source.** The plugin
protocol has no per-value sensitivity mark: `Sensitive` is a schema field, and
`ReadDataSourceResponse` carries only `State`/`Diagnostics`/`Deferred`. A single `value`
attribute is therefore *always* sensitive or *never*. `hashicorp/aws` chose always-sensitive
and bolted on `insecure_value`, which leaves a generator picking between two getters at synth
time on information it does not have. Splitting by CloudFormation type makes the choice
static: `cfncompat_ssm_parameter_value.value` and `cfncompat_ssm_parameter_list_value.values`
are **not** sensitive (CloudFormation itself publishes the resolved value in `DescribeStacks`,
so marking them sensitive would poison every downstream attribute for no CloudFormation-shaped
reason), and `cfncompat_ssm_secure_parameter_value.value` and
`cfncompat_secretsmanager_secret_value.value` **are**.

**4.2 Real list typing makes `Fn::Split` elision one mapping.** `values` is a genuine
`list(string)`, so `AWS::SSM::Parameter::Value<List<AWS::EC2::Subnet::Id>>` and the
`Fn.split(',', new CfnDynamicReference(SSM, name))` shape both map onto one node with one
typed getter. A `values` attribute that is null for a `String` parameter, or a
`DynamicAttribute`, would force the generated binding to `any` and break `for_each`/`length`
over an unknown value.

**4.3 CloudFormation's inner-type validation exists nowhere else.** Nothing in
`hashicorp/aws` knows that `AWS::SSM::Parameter::Value<AWS::EC2::Image::Id>` means "and check
that the AMI is real in this account and region". That check is a CloudFormation-engine
behaviour — and a real existence check, not a regex (T3c) — which is precisely what this
provider exists to polyfill; a `value_type` argument gives the generator somewhere to put the
type it already knows (§6).

**4.4 Explicit `version` plus `resolved_version` give pinning a home.** Live testing removed
the re-resolution *mismatch* for SSM (both CloudFormation paths re-resolve on every operation,
so Terraform's every-plan read is faithful — §7), but pinning remains a first-class
CloudFormation concept (`name:version` behind a typed parameter, T1; the `:version` segment of
a dynamic reference, T6f) and the only mitigation for the one path that *does* diverge,
Secrets Manager rotation. `resolved_version` is the read-back that makes pinning usable.

**4.5 Consistency with RFC 006.** `cfncompat_pseudo_parameters` and
`cfncompat_availability_zones` already live here despite `hashicorp/aws` having equivalents,
for the dependency-boundary reason: a CDK Terrain synthesis should need exactly two providers,
awscc and cfncompat. Reading an SSM parameter through `hashicorp/aws` would reintroduce the
third dependency for the single most common CDK deploy-time read there is (every
`MachineImage.latestAmazonLinux*`).

**4.6 One data source, two modes, because CloudFormation has two resolution paths.**
`cfncompat_ssm_parameter_value` is *not* split further, even though the live tests found the
typed-parameter and dynamic-reference paths differ in three ways (accepted Systems Manager
types, trimming, and the wording of the rejection). They are the same read of the same
parameter producing the same scalar, and a fifth data source would double the generated
bindings for a behavioural switch the backend already knows the answer to. So `value_type`
carries the discrimination and has **no default**:

| | `value_type` unset | `value_type` set |
|---|---|---|
| CloudFormation path | `{{resolve:ssm:name[:version]}}` | `AWS::SSM::Parameter::Value<T>` |
| Accepted SSM types | `String`, `StringList` | `String` only; `StringList` → *"…incompatible"* (T4d) |
| `StringList` value | raw comma-joined string, **untrimmed** (T6e) | n/a |
| `SecureString` | error: *"Non-secure ssm prefix was used for secure parameter X"* (T8d) | error: *"Parameters […] have types not supported by CloudFormation"* (T3e) |
| Inner-type validation | none | syntactic + existence, per `value_type` |

Giving `value_type` a `"String"` default would have silently put every unset configuration on
the *typed* path and broken the `{{resolve:ssm}}`-on-a-`StringList` case that CloudFormation
accepts. Absence is meaningful here, so absence is preserved.

Rejected alongside the fan-in design: `schema.DynamicAttribute` for the value (the framework's
own documentation prefers static types; an unresolved dynamic goes out as
`DynamicPseudoType`, which has no type for downstream expressions), a custom
`CommaDelimitedList` string type (the wire type stays `string`, so the generator gains
nothing), and a nested object result (same nullability problem, worse ergonomics).

## 5. `parse_dynamic_reference`: for references the backend cannot pre-parse

**The primary path involves no function at all.** In aws-cdk-lib a dynamic reference is not an
opaque string that the backend must recover structure from: it is a `CfnDynamicReference`
token built from a service plus structured key parts — `SecretValue.ssmSecure(name, version)`,
`valueForStringParameter(scope, name, version)`, `SecretValue.secretsManager(id, { jsonField,
versionStage })` — and `AWS::SSM::Parameter::Value<...>` is a `CfnParameter` carrying a type
and a name. A `TerraformIntrinsicResolver` (RFC 002 §I2) hooks the token, gets the parts for
free, and maps them straight onto data source arguments. Substring expansion — a reference
embedded in a larger string — is a synth-time token operation, already assigned to the backend
by RFC 006's "cfncompat owns the accessor, never the section rendering" split. Neither needs
Go-side parsing, and a literal hardcoded reference should never go through the function.

The function earns its keep in exactly the cases where the string's shape **cannot be known in
TypeScript**, so that only Go logic operating on the actual plan-time value can pull it apart:

1. **A whole-string token whose value arrives at plan or apply time** — a deploy-time
   Terraform variable, a value read from another resource's attribute, or a `CfnParameter`
   supplied at deploy time — that happens to carry a `{{resolve:...}}` string.
2. **Escape hatches**: `CfnResource.addPropertyOverride`, or a `cfn-include` template, where
   the reference is a concatenation of tokens rather than a literal the backend can inspect.

There the backend emits `provider::cfncompat::parse_dynamic_reference(<expr>)` and feeds the
resulting object's fields into the data source. Terraform evaluates the function at plan time
when the input is known, and defers the data source read to apply when it is not:

```hcl
variable "image_reference" {
  type    = string        # supplied at deploy time; may or may not be a reference
  default = "{{resolve:ssm:/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64}}"
}

locals {
  is_reference = provider::cfncompat::is_dynamic_reference(var.image_reference)
  reference    = local.is_reference ? provider::cfncompat::parse_dynamic_reference(var.image_reference) : null
}

data "cfncompat_ssm_parameter_value" "image" {
  count   = local.is_reference ? 1 : 0
  name    = local.reference.name
  version = local.reference.version == null ? null : tonumber(local.reference.version)
  # value_type stays unset: the input was a {{resolve:ssm}} reference, which is
  # the dynamic-reference resolution path (§4.6).
}

output "image_id" {
  value = local.is_reference ? data.cfncompat_ssm_parameter_value.image[0].value : var.image_reference
}
```

`is_dynamic_reference` exists because `parse_dynamic_reference` *errors* on a non-reference
rather than returning null (functions return `FuncError`, never a null result), so a
configuration that must branch on a plan-time value needs a total predicate first.

**Object contract.** The return is a `function.ObjectReturn` with a **fixed** seven-attribute
set, whatever the service turns out to be, so the type is statically known to Terraform and to
the generated bindings. Segments the service has no notion of, and segments the reference
omits, are `null`:

| Attribute | `ssm` / `ssm-secure` | `secretsmanager` |
|---|---|---|
| `service` | `"ssm"` / `"ssm-secure"` | `"secretsmanager"` |
| `name` | parameter name | secret id (name or full ARN) |
| `version` | version segment, **as a string** | `null` |
| `secret_string` | `null` | `"SecretString"` or `null` |
| `json_key` | `null` | `json-key` segment |
| `version_stage` | `null` | `version-stage` segment |
| `version_id` | `null` | `version-id` segment |

`version` is a string, not a number, so the attribute set never changes shape and an absent
version is `null` rather than a sentinel; the caller writes `tonumber(...)` when it needs the
`version` argument.

The function accepts **exactly one whole reference** — no surrounding text, no second
reference, no leading or trailing whitespace. That is deliberate: expanding an embedded
reference means emitting one data source per reference and rebuilding the string, which is the
backend's job, and a function has no way to return N data source reads.

**ARN disambiguation.** A `secret-id` may be a full ARN, which contains colons, inside a
colon-delimited reference. It is resolved positionally: an id starting with `arn:` consumes
exactly **seven** colon-separated parts
(`arn:<partition>:secretsmanager:<region>:<account>:secret:<name>-<suffix>`), and every part
after that is a positional segment. Seven is exact because Secrets Manager joins the
six-character random suffix to the name with a hyphen, not a colon. A string starting with
`arn:` that does not have seven parts is an error rather than being silently read as a plain
name.

## 6. Validation design

Every read applies, in order: the SSM/Secrets Manager call; the strict Systems Manager **type**
check for the mode in play (§4.6); the **syntactic** check of the declared `value_type` (always
on, no API call); its **existence** check (unless `validate = false`); then the optional
`allowed_pattern` / `allowed_values` arguments.

The existence check is the one CloudFormation actually performs — T3c confirmed that a
syntactically valid but non-existent `ami-0123456789abcdef0` fails identically to garbage, so
regex alone would be a strictly weaker polyfill. That is why `validate` defaults to **true**;
`validate = false` degrades to the syntactic check, which is a useful fallback when the plan
runs without EC2/Route 53 read permissions but is not what CloudFormation does. CloudFormation
performs the check at stack-level pre-resource validation, before any resource event, which
maps onto Terraform plan time.

The check is one **batched** API call for a whole list (Route 53 excepted: it has no batch
read, so `List<AWS::Route53::HostedZone::Id>` costs one call per distinct id). Duplicates are
collapsed before the call. A failed call is reported distinctly from a value that does not
exist, and always names the permission it needs.

### 6.1 `allowed_pattern` / `allowed_values` are an extension, not a polyfill

The single biggest surprise of the live tests: `AllowedPattern`, `AllowedValues`, `MinLength`
and `MaxLength` on an SSM-typed template `Parameter` validate the **raw parameter name** you
pass in, never the resolved value (T2). A pattern of `^hello-.*$` against a parameter whose
value is `hello-v2` is *rejected*; `^/cfncompat.*$`, which matches the name
`/cfncompat/livetest/str:2`, is *accepted*. CloudFormation therefore has **no** custom-regex
validation of resolved SSM values at all.

Consequences:

- `allowed_pattern` and `allowed_values` stay on the two non-secure data sources, applied to
  the **resolved value** (per element for a list), and are documented as an explicit cfncompat
  **extension** that diverges from CloudFormation. They are genuinely useful — a
  CloudFormation-shaped guard on a value is what an author reaching for them wants — but they
  are not fidelity.
- The synthesis backend must **not** map a `CfnParameter`'s constraints onto them. There the
  constraint applies to the literal name, which the backend already holds at synth time and can
  check itself, statically, with no provider involvement.
- `min_length` / `max_length` are **dropped**. With the CloudFormation analogy gone they are a
  bare length check on a string, which HCL can express without a provider argument.

### 6.2 IAM permissions

| Surface / `value_type` | Permissions |
|---|---|
| `cfncompat_ssm_parameter_value`, `..._list_value` (always) | `ssm:GetParameter` |
| `cfncompat_ssm_secure_parameter_value` (always) | `ssm:GetParameter` + `kms:Decrypt` on the parameter's key |
| `cfncompat_secretsmanager_secret_value` (always) | `secretsmanager:GetSecretValue` (+ `kms:Decrypt` for a customer-managed key) |
| `String`, `List<String>`, `CommaDelimitedList` | *(none beyond the read)* |
| `AWS::EC2::AvailabilityZone::Name` | `ec2:DescribeAvailabilityZones` |
| `AWS::EC2::Image::Id` | `ec2:DescribeImages` |
| `AWS::EC2::Instance::Id` | `ec2:DescribeInstances` |
| `AWS::EC2::KeyPair::KeyName` | `ec2:DescribeKeyPairs` |
| `AWS::EC2::SecurityGroup::GroupName` | `ec2:DescribeSecurityGroups` (via the `group-name` filter) |
| `AWS::EC2::SecurityGroup::Id` | `ec2:DescribeSecurityGroups` |
| `AWS::EC2::Subnet::Id` | `ec2:DescribeSubnets` |
| `AWS::EC2::Volume::Id` | `ec2:DescribeVolumes` |
| `AWS::EC2::VPC::Id` | `ec2:DescribeVpcs` |
| `AWS::Route53::HostedZone::Id` | `route53:GetHostedZone` |

`List<T>` needs exactly the same permission as `T`. `validate = false` removes every row below
the first three; the syntactic check still runs.

### 6.3 Binary-size impact

Measured on darwin/arm64, go1.27, `go build -o x .`, each service isolated by rebuilding the
finished provider with that service's usage stubbed out:

| Build | Bytes | Δ |
|---|---:|---:|
| Baseline (`origin/main`) | 79,840,882 | — |
| + `service/ssm` | +7,483,568 | +7.14 MiB |
| + `service/route53` | +3,147,440 | +3.00 MiB |
| + `service/secretsmanager` | +1,122,864 | +1.07 MiB |
| + six new EC2 operations (`DescribeImages`/`Instances`/`KeyPairs`/`SecurityGroups`/`Volumes`/`Vpcs`) | +32 | ~0 |
| + this RFC's own Go code | +184,320 | +180 KiB |
| **Total** | **91,779,106** | **+11,938,224 (+14.95%)** |

The EC2 additions are free because `service/ec2` is already linked for `Fn::GetAZs`
(RFC 006) and its serialisation is monolithic. `service/route53` costs 3 MiB for a single
`GetHostedZone` call, which is the most questionable line in the table. It is kept anyway:
`AWS::Route53::HostedZone::Id` is one of CloudFormation's ten AWS-specific types, and Design A
rests on the inner-type validation being *complete* — a type that silently skips the check
CloudFormation performs is worse than a 3.4% binary. Dropping it (and the type) is the obvious
lever if provider download size ever becomes a real constraint.

### 6.4 Fidelity table

Every row was verified against real CloudFormation stacks; the test ids index
[`dynamic-ssm/live-test-results.md`](dynamic-ssm/live-test-results.md).

| CloudFormation behaviour | Evidence | cfncompat |
|---|---|---|
| Typed parameter accepts `name:version`, incl. in `Default`; `:99` is a clean error | T1a/b/d/e | `version` argument; `ParameterVersionNotFound` mapped to a clear diagnostic |
| Labels unsupported everywhere (opaque `InternalFailure` behind a typed parameter; *"Incorrect format"* in a reference) | T1c, T6d, T8e | `label` kept as a documented **extension** (Systems Manager supports it); never emitted by the backend |
| Constraints validate the parameter **name**, not the resolved value | T2 | **Divergence, deliberate**: `allowed_pattern`/`allowed_values` apply to the value and are labelled an extension; `min_length`/`max_length` dropped (§6.1) |
| AWS-specific inner types are checked for **existence**, not just syntax, per element, at stack-level pre-resource validation | T3a/c/d/f, T4f | `validate = true` by default, one batched API call, at plan time; `validate = false` degrades to syntax only |
| `SecureString` behind a typed parameter → *"types not supported by CloudFormation"* | T3e | Same message, `value_type` set |
| `{{resolve:ssm}}` on a `SecureString` → *"Non-secure ssm prefix was used for secure parameter X"* | T8d | Same message, `value_type` unset |
| Strict declared-type match, content ignored (`String` ⇸ `List<…>`, `StringList` ⇸ `<String>`) → *"Types … are incompatible"* | T4b/c/d | Same message; list data source accepts **only** `StringList`, typed scalar mode only `String` |
| Typed `List<…>` resolution **trims** each element | T4a | `values` trimmed |
| `{{resolve:ssm}}` on a `StringList` returns the raw **untrimmed** string | T6e | `value` (mode a) and `raw_value` are verbatim |
| SSM-typed parameters re-resolve on **every** update, incl. `UsePreviousValue=true` no-ops | T5a/b/c | Read every plan — faithful |
| `{{resolve:ssm}}`/`{{resolve:ssm-secure}}` re-resolve on every deploy reaching the resource, even byte-identical | T6a/b/c | Read every plan — faithful |
| `{{resolve:secretsmanager}}` re-resolves **only** when the resource is independently updated | T9 | **Divergence**: read every plan, so a rotation shows a diff. Mitigate with `version_id` or `lifecycle { ignore_changes }` (§7) |
| Secrets Manager grammar: bare id, `:SecretString`, `:SecretString:key`, `…:AWSPREVIOUS`, full ARN + trailing segments | T9 | All parsed by `parse_dynamic_reference` and expressible on the data source |
| *"Could not find a value associated with JSONKey in SecretString"* / *"Could not parse SecretString JSON"*, resource-level | T9 | Both messages reused verbatim |
| Dynamic reference in an `Outputs` `Value` is returned unresolved | T9 | Backend emits nothing there (§3) |
| Unversioned reference in a `Parameters` `Default` → *"should not contain ssm versionless resolver"* | T7a/b | N/A — Terraform has no Parameters section; noted so a backend does not try |
| Mid-string, multiple references per string, `Fn::Sub` nesting all interpolate | T7c/d/e | Backend's job (§5); the function refuses non-whole references |
| `ssm-secure` in a non-allow-listed property → *"SSM Secure reference is not supported in: […]"* | T8a/b | Not enforced — the allow-list is documentation-sourced and property-shaped; documented on the data source |
| Public `/aws/service/...` parameters behave identically to private ones | T10 | No special-casing; the acceptance test uses one |

One question stayed open after testing. **What does `{{resolve:ssm-secure:...}}` do against a
plain `String` parameter?** T8c tried it and got the *allow-listed property* rejection instead
(*"SSM Secure reference is not supported in: [...]"*), because that check fires first and every
property cheap enough to test is off the allow-list; answering it properly needs an IAM or RDS
resource. `cfncompat_ssm_secure_parameter_value` therefore **warns and returns the value**
rather than erroring. The asymmetry with `cfncompat_ssm_parameter_value`, which errors on a
`SecureString`, is deliberate and safety-directed: exposing a secret through a non-sensitive
attribute is a security bug and must fail, while reading a plaintext value through a sensitive
attribute is merely over-cautious.

## 7. Plan-time semantics

Terraform reads a data source on **every plan** when its configuration is fully known, and
defers the read to apply when any argument is unknown (the normal defer-to-apply behaviour;
the framework's experimental `Deferred` mechanism is explicitly not used). Live testing settled
what that means against each CloudFormation path:

- vs `AWS::SSM::Parameter::Value<T>`: **faithful.** CloudFormation re-resolves on every stack
  operation, including a `UsePreviousValue=true` update that changes nothing else, and a change
  set with an empty `Changes` list still reports the fresh `ResolvedValue` (T5).
- vs `{{resolve:ssm:...}}` and `{{resolve:ssm-secure:...}}`: **faithful.** These re-resolve on
  every deploy that reaches the resource, even when the template text for it is byte-identical
  (T6a–c). The earlier mechanics study read the documentation's "also perform a stack update"
  advice as "CloudFormation does not re-resolve"; the live tests disprove that — *any* update
  re-resolves.
- vs `{{resolve:secretsmanager:...}}`: **the one real divergence.** CloudFormation re-resolves
  a Secrets Manager reference only when it is independently updating the resource that holds
  it; after a rotation, an unrelated stack update left the consumer on the old secret and
  produced no `UPDATE_*` event for it at all (T9). Terraform re-reads
  `cfncompat_secretsmanager_secret_value` on every plan, so a rotation immediately produces a
  diff on everything downstream. Two mitigations, both documented on the data source: pin
  `version_id`, which makes the read stable but defeats rotation exactly as CloudFormation's
  own docs warn; or put `lifecycle { ignore_changes = [...] }` on the consuming resource, which
  reproduces CloudFormation's "don't touch it unless something else changed" behaviour more
  precisely.

A plan and its apply can still straddle a parameter update when nothing is pinned;
`resolved_version` / `resolved_version_id` are the read-backs that make that visible.

**No batching.** Terraform core schedules data source reads as independent, parallel graph
nodes, and the plugin protocol has no cross-node batching hook: a provider cannot see that
twelve `cfncompat_ssm_parameter_value` nodes are about to run and collapse them into one
`GetParameters` call (which caps at ten names anyway). A template with many AMI lookups
therefore issues one `GetParameter` per node per plan; SSM throttling is handled by the AWS
SDK retryer configured on the provider's `aws.Config`. The only lever a provider *does* have
is per-process memoization of identical reads within one plan. **This slice deliberately does
not add it**: it would make a read's result depend on whether another node happened to run
first, and it is unmeasured — revisit only with a real throttling report.

## 8. State exposure, and the ephemeral follow-up

The two secret-reading data sources mark `value` `Sensitive`, which keeps it out of plan
output but **not** out of state: a data source result *is* state. That is an unavoidable
fidelity gap against `ssm-secure`, whose entire contract is that CloudFormation never stores
the value. Papering over it would be worse than naming it, so every successful read emits a
warning diagnostic that says what happens, how it differs from CloudFormation, and what
mitigates it (OpenTofu state encryption; an encrypted, least-privilege state backend; treating
state as a secret). Because the warning fires on every plan, `suppress_state_warning = true`
silences it once the trade-off has been accepted.

The real fix is ephemeral resources, which never enter plan or state and — unlike provider
functions — do get provider data and an AWS client. The blocker is downstream, not here: an
ephemeral value may only flow into provider configuration, another ephemeral resource, or a
resource's **write-only** argument, and the awscc provider exposes no write-only attributes at
all. So an ephemeral `cfncompat_ssm_secure_parameter_value` would today have nowhere legal to
send its value. Follow-up: ship it as a strict superset-of-safety sibling as soon as a
write-only consumer exists, and raise the awscc gap separately.

## 9. Open questions

- **Q1.** Should a future `cfncompat_ssm_parameter_pin` **resource** exist — one that captures
  a value at create and re-reads only when an input changes — to reproduce CloudFormation's
  genuine non-refresh behaviour for unversioned references? It is the only construct that does.
  Cost: a resource with no remote object plus a `triggers`-style refresh input. Deferred until
  a consumer's plan churn actually hurts.
- **Q2.** `AWS::SSM::Parameter::Name` — the "resolve the key, don't fetch the value" type — has
  no aws-cdk-lib L2 consumer. Skip permanently, or ship an existence-assertion read?
- **Q3.** Should `value_type` reject the combinations CloudFormation itself rejects but
  `GetParameter` allows — a `label`, or a cross-account ARN, in what the backend labelled a
  dynamic reference? Today the provider is a documented superset in both places.
- **Q4.** Secrets Manager `json_key` currently accepts a string, number or boolean value and
  rejects objects, arrays and `null`. The live tests covered a missing key and a non-JSON
  secret (T9) but not a non-scalar value; CloudFormation's behaviour there is still unknown.
- **Q5.** `{{resolve:ssm-secure:...}}` against a plain `String` parameter (§6.4) — needs an IAM
  or RDS fixture to test, since the allow-listed-property check fires first.
- **Q6.** Should the provider enforce CloudFormation's eleven-property `ssm-secure` allow-list?
  It is a property of the *consuming* resource, which a data source cannot see, so probably
  not — but a synthesis backend can and should.

## 10. Testing

- **Live CloudFormation** (36 stacks, us-east-1, all cleaned up): the evidence behind §6.4,
  recorded in [`dynamic-ssm/live-test-results.md`](dynamic-ssm/live-test-results.md).
- **Unit** (no AWS, no Terraform): SSM/Secrets Manager/EC2/Route 53 behind narrow interfaces
  with fakes — the value-type table itself (the exact scalar and list type sets, and the
  absence of `List<AWS::EC2::KeyPair::KeyName>`), the syntactic matrix for all ten AWS-specific
  types, each existence check's found/missing/denied/`validate=false` branches, list batching
  and duplicate collapsing, the `name:version` / `name:label` selector syntax, comma splitting
  and whitespace trimming including the empty-string degenerate case, the wrong-parameter-type
  errors and the data source each one points at, `ParameterNotFound` /
  `ParameterVersionNotFound` / `ResourceNotFoundException` / `InvalidRequestException`
  mapping, `json_key` extraction over every JSON value kind, `SecretBinary`-only rejection,
  the state warning and its suppression, the `version` ⨯ `label` `ValidateConfig` conflict,
  `ConfigErr` surfacing from `Read`, and the full dynamic-reference grammar including every
  documented example and the secret-ARN forms. The live tests are pinned as unit tests where
  they are observable offline: the two `value_type` modes and their distinct
  CloudFormation-shaped messages, `StringList`-in-dynamic-reference-mode returning the raw
  untrimmed string (T6e), the typed list path trimming `"a,b, c ,d"` to four elements (T4a),
  and the `String`-with-commas rejection (T4b/c).
- **Acceptance** (`TF_ACC=1` + `CFNCOMPAT_TEST_AWS=1`): `cfncompat_ssm_parameter_value` against
  `/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64`, a public parameter
  that exists in every commercial region and so needs no fixture, with
  `value_type = "AWS::EC2::Image::Id"` and a real `ec2:DescribeImages` validation; the
  pin-to-`resolved_version` round trip; the missing-parameter error; and the list data source
  on the same parameter. The two secret data sources are additionally gated on
  `CFNCOMPAT_TEST_SSM_SECURE_NAME` / `CFNCOMPAT_TEST_SECRET_ID`, since they need a fixture the
  operator creates and deletes. Both functions are pure, so their acceptance test needs no
  credentials at all.
