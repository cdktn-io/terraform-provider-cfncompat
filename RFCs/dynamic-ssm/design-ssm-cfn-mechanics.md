# CloudFormation SSM parameter references — mechanics study

Research input for a faithful `cfncompat_ssm_parameter_value` (and siblings) in
`terraform-provider-cfncompat`. Read-only study; no repo files were modified.

Primary citations (all verified in this session, 2026-09-02):

- P1 `https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/parameters-section-structure.html`
- P2 `https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/cloudformation-supplied-parameter-types.html`
- P3 `https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/dynamic-references.html`
- P4 `https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/dynamic-references-ssm.html`
- P5 `https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/dynamic-references-ssm-secure-strings.html`
- P6 `https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/dynamic-references-secretsmanager.html`
- P7 `https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_GetParameter.html`
- P8 `https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_Parameter.html`
- P9 `https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/aws-resource-ssm-parameter.html`
- P10 `https://docs.aws.amazon.com/systems-manager/latest/userguide/parameter-store-public-parameters.html`

---

## 1. The three CFN mechanisms

### 1.A Systems Manager template parameter types (`AWS::SSM::Parameter::Value<…>`)

**Syntax.** A `Parameters` entry whose `Type` is one of the SSM types. The value the *user*
supplies (or the `Default`) is the **Parameter Store key (name)**, not the value; CFN resolves
it to the value and `Ref` yields the resolved value (P2, "Overview": "anyone who uses your
template must specify a Parameter Store key as the value of the Systems Manager parameter type,
and CloudFormation then retrieves the latest value from Parameter Store").

**Supported types (exact list, P2 §"Supported Systems Manager parameter types"):**

| Template `Type` | Parameter Store `Type` required | `Ref` yields |
|---|---|---|
| `AWS::SSM::Parameter::Name` | any | the **name** — CFN "won't retrieve the actual value"; existence check only |
| `AWS::SSM::Parameter::Value<String>` | `String` | string |
| `AWS::SSM::Parameter::Value<List<String>>` | `StringList` | list of strings |
| `AWS::SSM::Parameter::Value<CommaDelimitedList>` | `StringList` | list of strings |
| `AWS::SSM::Parameter::Value<AWS-specific type>` e.g. `<AWS::EC2::Image::Id>`, `<AWS::EC2::KeyPair::KeyName>` | `String` | string, validated as that AWS type |
| `AWS::SSM::Parameter::Value<List<AWS-specific type>>` e.g. `<List<AWS::EC2::Subnet::Id>>` | `StringList` | list of strings, each validated |

The AWS-specific inner types are exactly the 10 scalar + 9 list types in P2
§"Supported AWS-specific parameter types": `AWS::EC2::AvailabilityZone::Name`,
`AWS::EC2::Image::Id`, `AWS::EC2::Instance::Id`, `AWS::EC2::KeyPair::KeyName`,
`AWS::EC2::SecurityGroup::GroupName`, `AWS::EC2::SecurityGroup::Id`, `AWS::EC2::Subnet::Id`,
`AWS::EC2::Volume::Id`, `AWS::EC2::VPC::Id`, `AWS::Route53::HostedZone::Id` (+ `List<…>` of
each except `KeyPair::KeyName`). aws-cdk mirrors this list verbatim in
`ParameterValueType` (`aws-ssm/lib/parameter.ts:243-299`).

**Not supported (P2 §"Unsupported"):**
- `List<AWS::SSM::Parameter::Value<String>>` (lists *of* SSM types).
- `SecureString` as a template parameter type — "CloudFormation doesn't support defining
  template parameters as `SecureString` Systems Manager parameter types."

**Resolution timing / re-resolution (P2 §Considerations — the load-bearing bullets):**
- "When you create or update stacks and create change sets, CloudFormation uses whatever value
  exists in Parameter Store at the time." → **resolved on every stack operation**, latest value.
- "For stack updates, when you use the **Use existing value** option (or set `UsePreviousValue`
  to true), this means that you want to keep using the same Parameter Store key, **not its
  value**. CloudFormation always retrieves the latest value." → there is **no pinning**; an
  update always re-reads. This is the single most important semantic for the Terraform mapping:
  the parameter-type mechanism is *latest-at-every-operation*, exactly like a Terraform data
  source read at every plan.
- Change-set caveat: "When you execute a change set, CloudFormation uses the values that are
  specified in the change set… they might change in Parameter Store between the time that you
  create the change set and run it." (i.e. resolution is snapshotted into the change set).
- Validation: "If you specify any allowed values or other constraints, CloudFormation validates
  them against the parameter **keys**… but not their values."
- Missing parameter → "CloudFormation returns a validation error."
- Cross-account: "For Parameter Store parameters shared by another AWS account, you must
  provide the full parameter ARN." (Contrast with dynamic references, which cannot do this
  at all — see 1.B.)

**Resolved value visibility.** `DescribeStacks`/`DescribeChangeSet` return `ResolvedValue` on
the `Parameter` struct: "Read-only. The value that corresponds to a Systems Manager parameter
key. This field is returned only for Systems Manager parameter types in the template." (P8).
So the resolved plaintext **is** visible in the API — consistent with SecureString being
disallowed here.

**List splitting.** CFN splits the `StringList` value on commas; `AWS::SSM::Parameter` itself
documents "If type is `StringList`, the system returns a comma-separated string with no spaces
between commas in the `Value` field" (P9). The CLI-side escaping (`\\,`) in P2 is about passing
*list parameter values* on the command line, not about SSM.

**No version selector.** P2 never mentions `name:version` for the parameter-type mechanism, and
aws-cdk's implementation confirms it: `StringParameter.fromStringParameterAttributes`
(`aws-ssm/lib/parameter.ts:560-590`) emits a `CfnParameter` with
`type: AWS::SSM::Parameter::Value<...>, default: parameterName` **only when no version is
given**; if `attrs.version` is set it switches to a `{{resolve:ssm:name:version}}` dynamic
reference instead. Treat "versioned selection via the parameter type" as **unsupported /
unverified** (open question O3).

**`NoEcho`.** `NoEcho` masks the value in describe calls (P1). It is orthogonal to the SSM
type; since SecureString is unsupported here, `NoEcho` + SSM type is possible but pointless.
P1's important caveats: `NoEcho` does *not* mask `Metadata`, `Outputs`, or resource `Metadata`,
and CFN "may use the actual plaintext value in the primary resource identifier."

### 1.B Dynamic references `{{resolve:…}}`

**Common rules (P3 §General considerations):**
- ≤ 60 dynamic references per template.
- Not resolved before transforms (`AWS::Include`, `AWS::Serverless`) — the literal string is
  passed to the transform.
- **Not supported** in `AWS::CloudFormation::Init` metadata and EC2 `UserData`.
- Dynamic references for *secure* values are not supported in custom resources (but plain
  `ssm` **is**: "For custom resources, CloudFormation resolves the `ssm` dynamic references
  before sending the request to the custom resource", P4).
- A reference must not end with a backslash.

**`{{resolve:ssm:parameter-name:version}}` (P4):**
- Regex: `{{resolve:ssm:[a-zA-Z0-9_.\-/]+(:\d+)?}}`.
- Applies to Parameter Store `String` **or** `StringList` types. The result is a **string**;
  for a StringList you get the comma-joined string and must split it yourself (aws-cdk does
  exactly this: `Fn.split(',', new CfnDynamicReference(SSM, name))`,
  `aws-ssm/lib/parameter.ts:772`).
- `version` optional; "If you don't specify the exact version, CloudFormation uses the latest
  version of the parameter whenever you create or update the stack."
- **Drift/refresh**: "CloudFormation doesn't support drift detection on dynamic references. For
  `ssm` dynamic references where you haven't specified the parameter version, we recommend
  that, if you update the parameter version in Systems Manager, you **also perform a stack
  update operation** on any stacks that include the `ssm` dynamic reference, in order to fetch
  the latest parameter version." → CFN does **not** re-resolve on its own; the value is
  refreshed only when a stack operation touches the template/resource. (Same shape is stated
  explicitly for `secretsmanager` in P6: "Updating only the secret value in Secrets Manager
  doesn't automatically cause CloudFormation to retrieve the new value… only during resource
  creation or updates that modify the resource containing the dynamic reference.")
- **In the `Parameters` section**: allowed **only with an explicit version number** —
  "To use a `ssm` dynamic reference in the `Parameters` section of your CloudFormation
  template, you must include a version number." The recommended alternative is mechanism 1.A.
- No parameter **labels**: "CloudFormation doesn't support using Systems Manager parameter
  labels in dynamic references."
- No cross-account: "CloudFormation doesn't support using dynamic references to reference a
  parameter shared from another AWS account."
- Which version a stack op will use is inspectable by creating a change set and reading the
  processed template (P4).
- Public parameters work: the documented example is
  `{{resolve:ssm:/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-6.1-x86_64}}`.

**`{{resolve:ssm-secure:parameter-name:version}}` (P5):**
- Regex `{{resolve:ssm-secure:[a-zA-Z0-9_.\-/]+(:\d+)?}}`; `version` is **optional today**
  (historically required — current docs say "Optional", and aws-cdk's `SecretValue.ssmSecure`
  takes `version?`, `core/lib/secret-value.ts:152-156`).
- "CloudFormation never stores the actual secure string value. Instead, it only stores the
  literal dynamic reference… CloudFormation doesn't return the actual parameter value for
  secure strings in any API calls." Change sets compare **the literal reference string**, not
  the resolved value.
- **Allowed only in an allow-listed set of resource properties** (P5 table, verbatim):
  `AWS::DirectoryService::MicrosoftAD.Password`, `AWS::DirectoryService::SimpleAD.Password`,
  `AWS::ElastiCache::ReplicationGroup.AuthToken`, `AWS::IAM::User.LoginProfile.Password`,
  `AWS::KinesisFirehose::DeliveryStream.RedshiftDestinationConfiguration.Password`,
  `AWS::OpsWorks::App.Source.Password`, `AWS::OpsWorks::Stack.CustomCookbooksSource.Password`,
  `AWS::OpsWorks::Stack.RdsDbInstances.DbPassword`, `AWS::RDS::DBCluster.MasterUserPassword`,
  `AWS::RDS::DBInstance.MasterUserPassword`, `AWS::Redshift::Cluster.MasterUserPassword`.
- No labels, **no public parameters**, no cross-account; not supported in `cn-north-1` /
  `cn-northwest-1`; not supported in custom resources.
- Rollback hazard: if the previously used version no longer exists, rollback fails.

**`{{resolve:secretsmanager:secret-id:SecretString:json-key:version-stage:version-id}}` (P6)**
— for completeness. Defaults: `SecretString`, whole secret if no `json-key`, `AWSCURRENT` if
neither stage nor id. Usable in **all** resource properties (unlike `ssm-secure`). Cross-account
works if you pass the full ARN.

**Substring substitution.** Dynamic references are string-level: the resolver replaces the
`{{…}}` token inside a larger string, which is why `MasterUsername:
'{{resolve:secretsmanager:MySecret:SecretString:username}}'` and AMI-in-a-string patterns work,
and why `Fn::Sub` composes with them (the `Fn::Sub` output string may itself contain a
`{{resolve:…}}` token, resolved after `Fn::Sub` produces the string). Note CFN's own
`ResolveSsmParameterAtLaunchImage` variant in aws-cdk emits a **bare** `resolve:ssm:name:ver`
string (no braces) because EC2 — not CFN — does that resolution at instance launch
(`aws-ec2/lib/machine-image/machine-image.ts:246-268`).

### 1.C `AWS::SSM::Parameter` resource (value shapes only)

From P9: `Type` is **required**, allowed values `String | StringList` — "Parameters of type
`SecureString` are not supported by AWS CloudFormation." `DataType` allowed values
`text | aws:ec2:image` (default `text`); `aws:ec2:image` makes SSM validate that the value is
a real AMI id. `Tier`: `Standard | Advanced | Intelligent-Tiering`. `Value` is a plain string;
a `StringList` value is "a comma-separated string with no spaces between commas". `Ref` returns
the parameter **name**. aws-cdk mirrors these as `ParameterType`, `ParameterDataType`,
`ParameterTier` (`aws-ssm/lib/parameter.ts:304-356`).

---

## 2. SSM API surface the data source needs

`GetParameter` (P7) — request `{ Name, WithDecryption }`:

- **`Name`** — "The name or Amazon Resource Name (ARN) of the parameter… For parameters shared
  with you from another account, you must use the full ARN." Selector syntax is *in the Name
  field*: "To query by parameter label, use `\"Name\": \"name:label\"`. To query by parameter
  version, use `\"Name\": \"name:version\"`." Length 1–2048.
- **`WithDecryption`** — "Return decrypted values for secure string parameters. This flag is
  ignored for `String` and `StringList` parameter types." Not required.
- Response `Parameter`: `ARN`, `DataType` (`text` | `aws:ec2:image`), `LastModifiedDate`,
  `Name`, `Selector`, `SourceResult`, `Type` (`String` | `StringList` | `SecureString`),
  `Value`, `Version`.
- Errors: `ParameterNotFound` (400; note: not recorded in CloudTrail for GetParameter),
  `ParameterVersionNotFound` (400), `InvalidKeyId` (400), `InternalServerError` (500).
  Throttling: `ThrottlingException: Rate exceeded` — relevant because a Terraform plan may
  issue N of these per plan; consider `GetParameters` (batch, up to 10) if we ever fan in.
- Public parameters (P10) are read with the same `GetParameter` call, by their `/aws/service/…`
  path; discovery is `GetParametersByPath` on `/aws/service`.

Facts that matter for fidelity:
- The API always returns `Value` as a **string**, including for `StringList` (comma-joined).
  Any list typing is our own split, matching CFN.
- `DataType: aws:ec2:image` is the only signal that the stored value is an AMI id; CFN's
  `AWS::SSM::Parameter::Value<AWS::EC2::Image::Id>` validation is a *template parameter type*
  check, not a `DataType` check — they are independent and can disagree.

---

## 3. aws-cdk-lib consumption inventory

Grep over `packages/aws-cdk-lib` (excluding tests) for `valueForStringParameter`,
`valueForTypedStringParameterV2`, `valueForTypedListParameter`, `valueFromLookup`,
`SecretValue.ssmSecure`, `AWS::SSM::Parameter::Value`, `CfnDynamicReferenceService.SSM`:

| Call site | file:line | Mechanism | Value type | Secure |
|---|---|---|---|---|
| `StringParameter.valueForStringParameter` | `aws-ssm/lib/parameter.ts:646` | delegates to V2 → 1.A (`<String>`) | string | no |
| `StringParameter.valueForTypedStringParameterV2` | `aws-ssm/lib/parameter.ts:657` | 1.A `<type>`, or 1.B if `version` given | string | no |
| `StringParameter.valueForTypedStringParameter` (deprecated) | `parameter.ts:675` | same; rejects `STRING_LIST` | string | no |
| `StringParameter.fromStringParameterAttributes` | `parameter.ts:560-590` | 1.B when `version` or `forceDynamicReference` or token-name; else 1.A | string | no |
| `StringParameter.fromSecureStringParameterAttributes` | `parameter.ts:597-612` | 1.B `ssm-secure` | string | **yes** |
| `StringParameter.valueForSecureStringParameter` (deprecated) | `parameter.ts:696` | 1.B `ssm-secure`, version **required** | string | **yes** |
| `StringParameter.valueFromLookup` | `parameter.ts:626` | **neither** — synth-time context provider (`cdk.context.json`) | string | no |
| `StringListParameter.fromStringListParameterName` | `parameter.ts:767-775` | 1.B + `Fn::Split(',')` | list | no |
| `StringListParameter.fromListParameterAttributes` | `parameter.ts:781-800` | 1.A `<List<type>>` (no version) / 1.B `.toStringList()` (version) | list | no |
| `StringListParameter.valueForTypedListParameter` | `parameter.ts:810` | as above | list | no |
| `SecretValue.ssmSecure(name, version?)` | `core/lib/secret-value.ts:152` | 1.B `ssm-secure` | string (SecretValue) | **yes** |
| `SecretValue.secretsManager` | `core/lib/secret-value.ts:97` | 1.B `secretsmanager` | string (SecretValue) | **yes** |
| Bootstrap-version rule (`BootstrapVersion` CfnParameter) | `core/lib/stack-synthesizers/stack-synthesizer.ts:340` | 1.A `<String>` | string | no |
| Cross-region export writer | `core/lib/custom-resource-provider/cross-region-export-providers/export-writer-provider.ts:135` | 1.B `ssm` | string | no |
| `GenericSSMParameterImage.getImage` (all `MachineImage.latestAmazonLinux*`, Windows, etc.) | `aws-ec2/lib/machine-image/machine-image.ts:228` | 1.A `<AWS::EC2::Image::Id>` | string (AMI id) | no |
| `lookupImage` (EC2 `cachedInContext` switch) | `aws-ec2/lib/machine-image/utils.ts:5-8` | context lookup **or** 1.A `<AWS::EC2::Image::Id>` | string | no |
| `ResolveSsmParameterAtLaunchImage` | `aws-ec2/lib/machine-image/machine-image.ts:262` | bare `resolve:ssm:name[:ver]` string — **EC2-side** resolution, not CFN | string | no |
| ECS optimized AMI (`amis.ts` lookupImage) | `aws-ecs/lib/amis.ts:466-467` | context lookup **or** 1.A `<AWS::EC2::Image::Id>` | string | no |
| ECS FireLens/fluent-bit image | `aws-ecs/lib/firelens-log-router.ts:209` | 1.A `<String>` | string | no |
| EKS optimized AMI | `aws-eks/lib/cluster.ts:2937`, `aws-eks-v2/lib/cluster.ts:2351` | 1.A `<String>` (comment: typed String "for historical reasons") | string | no |
| EKS Bottlerocket AMI | `aws-eks/lib/private/bottlerocket.ts:42`, `aws-eks-v2/…:42` | 1.A `<String>` | string | no |

Type→shape rule used by CDK core when rendering a `CfnParameter`
(`core/lib/cfn-parameter.ts:355-371`): a type is a **list** iff it contains `List<` or
`CommaDelimitedList`; a **number** iff exactly `Number`; else **string**. So
`AWS::SSM::Parameter::Value<List<AWS::EC2::Subnet::Id>>` → list, everything else SSM → string.
That heuristic is a good candidate to reuse verbatim on the provider side.

Consumer motivation (already recorded in the fleet docs):
- `cdktn-awscc/docs/bridge-gap-categories.md:148-156` — CFN dynamic parameter types
  "are a CFN-engine feature resolved at deploy time — exactly cfncompat's vehicle (§6): **spike a
  `cfncompat_ssm_parameter_value`-style data-source polyfill**", and explicitly notes the two
  aws-cdk mechanisms resolve at *different times* (`valueFromLookup` = synth-time context
  provider, never cfncompat's business; `valueForStringParameter` = deploy-time).
- Same file line 336: "an `ssm_parameter_value`-style read for CFN dynamic parameter types".
- `s3-notifications-harness/docs/cfn-intrinsics-survey.md:71-79` — `CfnParameter` incl.
  SSM-backed types; the framework's own `BootstrapVersion` use should simply not fire under a
  bootstrapless synthesizer.

---

## 4. Implications for a Terraform data source design

### 4.1 Requirements a CFN-faithful implementation must satisfy

R1. **Name selector fidelity.** Accept a bare name, a path name (`/a/b/c`), a full ARN
    (cross-account), and the `name:version` / `name:label` selector forms that `GetParameter`
    supports (P7). But note CFN itself rejects labels in dynamic references (P4) and rejects
    cross-account in dynamic references while *allowing* an ARN under mechanism 1.A (P2) — the
    provider is a superset, so the fidelity decision is whether to warn/error on
    CFN-illegal combinations. Recommend: support the superset, document the divergence.

R2. **Version pinning must be expressible**, because 1.B with an explicit version is a
    distinct CFN semantic (pinned, rollback-relevant) from 1.B/1.A without one (latest at
    operation time).

R3. **Value typing per inner type.** Three output shapes are needed:
    - scalar string (`<String>`, `<AWS-specific type>`, `{{resolve:ssm:…}}` on a `String`),
    - list of strings (`<List<String>>`, `<CommaDelimitedList>`, `<List<AWS-specific>>`) —
      obtained by splitting the API's comma-joined value,
    - the "name only, don't fetch the value" shape (`AWS::SSM::Parameter::Name`) — which is an
      **existence assertion**, best served by a data source that reads the parameter and
      exposes `name`/`arn` without the caller consuming `value`.

R4. **AMI-id validation.** CFN validates `<AWS::EC2::Image::Id>` against the account/region.
    Faithful-but-cheap option: regex-validate `ami-[0-9a-f]{8,17}` and surface `data_type`
    (`text` vs `aws:ec2:image`) so callers can assert; a real `DescribeImages` check is what CFN
    does but costs an extra API call + permission. Recommend regex + `data_type` exposure only,
    with an optional `validate` toggle deferred (open question O5).

R5. **Sensitivity.** A `SecureString` value must never be written unmarked. `ssm-secure`'s CFN
    contract ("CloudFormation never stores the actual secure string value", P5) is *not*
    reproducible in Terraform — Terraform state always holds the value in plaintext, exactly as
    `terraform-provider-aws` warns ("The unencrypted value of a SecureString will be stored in
    the raw state as plain-text"). This must be documented as an explicit, unavoidable fidelity
    gap, not papered over.

R6. **Non-poisoning escape hatch.** `terraform-provider-aws` marks `value` sensitive
    *always, regardless of type*, and added `insecure_value` (null for `SecureString`) so that
    plain `String` values don't poison every downstream attribute with sensitivity. Since CFN's
    mechanism 1.A explicitly exposes the resolved value in `DescribeStacks.ResolvedValue` (P8),
    a CFN-faithful data source should present non-secure values as **non-sensitive by default**
    — the opposite default from `hashicorp/aws`. That is a deliberate divergence worth naming.

R7. **Resolution timing.** Terraform data sources read at **every plan**. That is *stronger*
    re-resolution than either CFN mechanism:
    - vs 1.A: actually equivalent in outcome (CFN also re-reads on every operation), except
      that a Terraform plan happens more often than a stack operation and will surface a diff
      the moment the SSM value changes, where CFN would only notice at the next deploy.
    - vs 1.B without a version: **materially different**. CFN explicitly does not re-resolve
      unless a stack operation touches the resource (P4's drift bullet, P6's rotation bullet).
      A data source will show churn where CFN shows none.
    Three ways to bridge this, none free:
      (a) accept the difference (document it) — the `hashicorp/aws` precedent, which chose
          "data source = always latest";
      (b) require/encourage a `version` argument, which makes both models identical;
      (c) offer a *resource* (`cfncompat_ssm_parameter_value` as a managed resource) that
          captures the value in state at create and only re-reads when an input changes —
          this is the only construct that genuinely reproduces "resolve once, don't
          re-resolve on unrelated updates". Cost: a resource with no remote object, plus a
          `lifecycle`/`triggers`-style refresh input.
    Recommendation: ship the data source now (a/b), keep (c) as a documented future
    `cfncompat_ssm_parameter_pin` if a real consumer needs CFN's non-refresh behaviour.

R8. **Substring dynamic references.** `{{resolve:ssm:…}}` can appear *inside* a larger string.
    Two possible owners:
    - the **synthesis backend** splits the string and emits
      `"prefix${data.cfncompat_ssm_parameter_value.x.value}suffix"`; or
    - the provider ships a function `provider::cfncompat::resolve_dynamic_reference(string)`.
    A provider *function* cannot make AWS API calls in the general case (functions in
    terraform-plugin-framework are pure and have no provider-configured client), so option 2 is
    likely **not implementable** as a value-fetching function — flag as Q6 for the
    plugin-framework study. Given RFC 006's stated ownership split ("cfncompat owns the
    *accessor function*, never the *section rendering*"), string splitting is the **backend's**
    job; the provider only supplies the read. Recommend: backend-side splitting, no
    `resolve_dynamic_reference` function.

R9. **The `AWS::SSM::Parameter::Value<…>` template-parameter case.** Two sub-cases:
    - CDK emitted the `CfnParameter` purely as the *vehicle* for an SSM read (the
      `valueForStringParameter` family, which is the overwhelming majority — see §3). The
      backend should **not** render a `TerraformVariable` at all; it should render a
      `cfncompat_ssm_parameter_value` data source keyed on the parameter's `Default` (the SSM
      name) and wire `Ref` to `.value` / `.values`.
    - A genuinely user-authored SSM-typed `CfnParameter` where the *name* is meant to be an
      input. Then: `TerraformVariable` (the name) + data source (the read) keyed on it.
    Both need the list/scalar discrimination from `core/lib/cfn-parameter.ts:355-371`.
    `AWS::SSM::Parameter::Name` → variable only, optionally + an existence-checking read.

R10. **`NoEcho` propagation.** If the CFN parameter carries `NoEcho: true`, the backend should
    mark the corresponding Terraform variable `sensitive = true`. For the data source, `NoEcho`
    has no analogue — sensitivity is decided by the parameter's SSM `Type` (R5/R6).

R11. **Error surface.** `ParameterNotFound` and `ParameterVersionNotFound` must be distinct,
    actionable diagnostics (CFN returns a validation error for a missing key, P2). Throttling
    should be retried (SDK default) and reported clearly.

R12. **Region/credential handling** should match `cfncompat_availability_zones`: an optional
    `region` argument overriding the provider default, built through the same
    `newClient(region)` seam used in `data_source_availability_zones.go` for testability.

### 4.2 Candidate designs

**D1 — one data source, `type` discriminator, both outputs.**
`data "cfncompat_ssm_parameter_value" { name, version?, with_decryption? }` →
`value` (string), `values` (list(string), the comma-split), `type`, `data_type`, `version`,
`arn`, `last_modified_date`, `name`.
- Pros: one node per parameter regardless of shape; mirrors CFN, where one Parameter Store
  parameter feeds both `<String>` and `<List<String>>` template types; matches RFC 006's
  precedent of one fan-in data source (`cfncompat_pseudo_parameters`) rather than N; the
  backend picks `.value` or `.values` using CDK's own list heuristic; one binding class in the
  generated cdktn bindings.
- Cons: `values` is always computed even for a `String` (would be a 1-element list — arguably
  correct: CFN would reject the template, we just return something); sensitivity is per-
  attribute, so a `SecureString` read makes `value` sensitive while `values` must also be
  marked (typing question Q1/Q2 below).

**D2 — three data sources.**
`cfncompat_ssm_parameter_value` (string), `cfncompat_ssm_parameter_list_value` (list(string)),
`cfncompat_ssm_secure_parameter_value` (always-sensitive string).
- Pros: each has an unambiguous, statically-typed output; sensitivity is a property of the
  *data source*, not of a runtime branch — much easier to reason about and to document; maps
  1:1 onto the three CFN template shapes and onto `ssm` vs `ssm-secure`; a `ssm-secure` data
  source can carry the P5 allow-list warning in its docs.
- Cons: three schemas, three docs pages, three generated bindings; the backend needs the
  discrimination logic anyway; a caller who genuinely doesn't know the type at synth time
  (rare — CDK always knows) can't pick.

**D3 — data source + provider function `resolve_dynamic_reference(string)`.**
- Pros: would let a rendered property string containing `{{resolve:ssm:…}}` be handled with no
  backend string-splitting; conceptually closest to CFN's own resolver.
- Cons: almost certainly not implementable — provider functions have no AWS client and must be
  pure (Q6); duplicates ownership the fleet docs already assign to the backend/renderer; would
  need a second, impure escape hatch anyway for the actual fetch. **Reject**, except possibly a
  *pure* helper that **parses** a dynamic reference string into its parts
  (`provider::cfncompat::parse_dynamic_reference`) so the backend and provider agree on the
  grammar — cheap, testable, no API calls.

**Recommendation: D2, with D1's fan-in as the fallback if the plugin-framework study says a
single schema with a dynamically-typed output is clean.** Rationale: sensitivity is the hardest
part of this design (R5/R6), and D2 makes sensitivity a static schema property rather than a
runtime decision, which is both simpler in the framework and clearer in `terraform plan` output.
The secure variant also has a genuinely different contract (P5 allow-list, no public
parameters, no labels, cn-region gap) that deserves its own docs page. Add the pure
`parse_dynamic_reference` function from D3 only if the backend actually wants it.

### 4.3 Questions for the plugin-framework study

- Q1. Can a single schema attribute be marked sensitive **conditionally at read time**, or is
  `Sensitive: true` necessarily static in `datasource/schema`? (Decides D1 vs D2.)
- Q2. If a `types.List` element comes from a sensitive source, does the framework propagate
  sensitivity to the whole list, or must each element be handled? What does Terraform core do
  with a sensitive collection in plan output?
- Q3. Is there an idiomatic way to expose "value as string **or** list" — e.g. a
  `types.Dynamic` attribute — and what does that do to the generated cdktn/CDKTF bindings and
  to `terraform validate` in a module consumer?
- Q4. Can a data source declare a `version` argument that, when set, makes the read cacheable /
  non-refreshing across plans? (I.e. is there any framework-level equivalent of "don't re-read
  when inputs are unchanged" for data sources, or is per-plan read unconditional?)
- Q5. For a future pinning **resource** (R7c) with no remote object: what is the recommended
  shape — `RequiresReplace` plan modifiers on the inputs plus a `UseStateForUnknown` on the
  value? Any precedent in the ecosystem (e.g. `time_static`, `random_*`)?
- Q6. Can a provider-defined **function** access provider configuration / an AWS client, or are
  functions strictly pure? (Kills or enables D3's fetching variant.)
- Q7. Does the framework offer a first-class way to emit a *warning* diagnostic from a data
  source read (e.g. "this combination is illegal in CloudFormation") without failing the plan?

---

## 5. Open questions for the user

- O1. **Scope of the first cut.** Just `cfncompat_ssm_parameter_value` (mechanism 1.A/1.B
  non-secure), or the full D2 trio including the secure variant on day one? The secure variant
  cannot be CFN-faithful (R5) — is shipping it with a prominent "value lands in state" warning
  acceptable, or should cfncompat refuse to read `SecureString` at all and force users to
  `hashicorp/aws`?
- O2. **Sensitivity default.** Diverge from `hashicorp/aws` (non-secure values non-sensitive, as
  CFN's `ResolvedValue` is public) or match it (always sensitive + an `insecure_value` twin)?
  Divergence is more CFN-faithful; matching is less surprising to Terraform users.
- O3. **Is `AWS::SSM::Parameter::Value<String>` with a `name:version` default actually
  supported by CFN?** Undocumented; aws-cdk avoids it. Worth a 10-minute live test against a
  real stack before we decide whether the data source's `version` argument has a 1.A analogue.
- O4. **Does the backend ever need the "name only" mechanism (`AWS::SSM::Parameter::Name`)?**
  No aws-cdk-lib L2 uses it. Skip entirely, or ship an `exists`-style read?
- O5. **AMI-id validation depth.** Regex + `data_type` only, or an optional `DescribeImages`
  check matching CFN's account/region validation (extra IAM permission, extra call per plan)?
- O6. **Batching.** Should the data source ever fan in multiple names (`names = [...]` →
  `GetParameters`, 10 per call) to keep plan-time API volume and throttling risk down for
  templates with many AMI lookups, in the spirit of `cfncompat_pseudo_parameters`' one-call
  design? Or keep it strictly one parameter per data source for graph clarity?
- O7. **Secrets Manager.** Do we also want `cfncompat_secretsmanager_secret_value` for
  `{{resolve:secretsmanager:…}}` (`SecretValue.secretsManager`, used widely by RDS/CodePipeline
  L2s), or is `hashicorp/aws`'s `aws_secretsmanager_secret_version` good enough there? The
  awscc-only motivation argues for shipping it; the sensitivity problem is identical.
- O8. **RFC number.** Presumably `RFCs/007-ssm-parameter-polyfill.md`, following 006's format
  (Status/Companion/Origin table, "Decision" §1, per-data-source §, testing §).
