# Terraform typing study for `cfncompat_ssm_parameter_value`

Framework: `terraform-plugin-framework@v1.19.0`
(`/Users/vincentsmet/go/pkg/mod/github.com/hashicorp/terraform-plugin-framework@v1.19.0`, cited as `FW/`).
Protocol: `terraform-plugin-go@v0.31.0` (`PG/`).
Provider: `/Users/vincentsmet/cdktn/terraform-provider-cfncompat` (`P/`).

Consumer of record is a **code generator** (CDK-shaped TypeScript emitting HCL/JSON), so
statically-known attribute types beat HCL-author convenience everywhere below.

---

## 1. Attribute typing options for "string OR list"

### 1a. Two separate computed attributes (`StringAttribute` + `ListAttribute`)

- `FW/datasource/schema/string_attribute.go:57-62` — `Sensitive bool`; `:180-182` `IsSensitive()`.
- `FW/datasource/schema/list_attribute.go:47-53` — `ElementType attr.Type`, "must be set" ;
  `:55-58` `CustomType basetypes.ListTypable`; `:76-81` `Sensitive`;
  `:233-234` schema validation raises `AttributeMissingElementTypeDiag` when neither
  `CustomType` nor `ElementType` is set.
- A computed attribute may be **null at runtime** with no schema-level ceremony: nullability is a
  value-state property (`attr.ValueStateNull`), not a schema flag. There is no `Required`-style
  guard forcing a computed attribute to be non-null. This is exactly what makes the
  `value` / `insecure_value` pattern in `hashicorp/aws` legal (see §2).

Verdict: fully static types, both attributes present in the schema at all times, one of them null
per read. Ideal for a generator: `DataCfncompatSsmParameterValue.value` is `string` and
`.values` is `string[]` in the emitted TS binding, unconditionally.

### 1b. `schema.DynamicAttribute` (types.Dynamic, v1.7+)

- `FW/datasource/schema/dynamic_attribute.go:23-35` — the framework's own warning:
  *"Static types are always preferable over dynamic types in Terraform as practitioners will
  receive less helpful configuration assistance from validation error diagnostics and editor
  integrations."* Concrete type is decided **1. by Terraform if configured, 2. by the provider if
  Computed** and *"Once the concrete value type has been determined, it must remain consistent
  between plan and apply or Terraform will return an error."*
- Fields: `CustomType basetypes.DynamicTypable` (`:40`), `Computed` (`:56`), `Sensitive` (`:63`),
  `Validators []validator.Dynamic` (`:123`).
- `FW/types/basetypes/dynamic_type.go:26-33` repeats the "static types are always preferable"
  guidance; `:36-40` — `ApplyTerraform5AttributePathStep` **always errors**, i.e. you cannot
  path-step into a dynamic in the schema, only through the runtime `DynamicValue`.
- `FW/types/basetypes/dynamic_value.go:43-55` `NewDynamicValue`, `:57-63` `NewDynamicNull`
  (wire type is `tftypes.DynamicPseudoType`), `:65-67` `NewDynamicUnknown`,
  `:167-173` `UnderlyingValue`, `:175-185` `IsUnderlyingValueNull`,
  `:187-192` `IsUnderlyingValueUnknown`.

Drawbacks for our case, in order of severity:

1. **Not known at plan time when the read is deferred.** A computed dynamic that the provider has
   not yet resolved goes out as `NewDynamicUnknown()` → wire `DynamicPseudoType`. Downstream
   expressions have *no type*, so `for_each`, `length()`, splat and object construction over it
   fail at plan. With a static `list(string)` the type survives even when the value is unknown.
2. **The generator cannot type the binding.** `cdktn`/jsii codegen would have to emit `any`
   (`IResolvable`) for `.value` and force every call site through a cast. That defeats the whole
   point of the CDK-shaped surface.
3. Type must stay identical plan→apply (`:34-35`). If the parameter's SSM type changes between
   plan and apply (String → StringList), Terraform hard-errors instead of showing a diff.

Verdict: **reject** for the value payload. Dynamic is defensible only if we ever need to echo an
arbitrary user-supplied CFN parameter shape back verbatim, which we don't.

### 1c. Custom types (`CustomType` + `StringTypable` / `StringValuable`)

- `FW/types/basetypes/string_type.go:15-22` — `StringTypable` = `attr.Type` + `ValueFromString`.
- `FW/types/basetypes/string_value.go:29-42` — `StringValuableWithSemanticEquals` with
  `StringSemanticEquals(ctx, StringValuable) (bool, diag.Diagnostics)`; *"Only known values are
  compared with this method."* `FW/types/basetypes/list_value.go:30-43` is the list analogue.
- Validation on a custom type: **`xattr.TypeWithValidate` is deprecated**
  (`FW/attr/xattr/type.go:16-30`: *"Deprecated: Use the ValidateableAttribute interface instead…"*).
  The live interface is `xattr.ValidateableAttribute` (`FW/attr/xattr/attribute.go:13-21`) —
  implemented on the **Value**, called implicitly when Terraform values are converted into
  framework types.

Where custom types actually pay off here: **nowhere on the data source's outputs**. Semantic
equality only matters for *resource* drift suppression; a data source is re-read every plan and its
value is never compared against prior state for diffing. An AMI-id-validating custom type would
validate the *provider's own* output, which is pointless — validate the input instead (§5).
A `CommaDelimitedList` custom string type is worse than useless: it would keep the wire type
`string` while pretending to be a list, so the generator still sees `string`.

Verdict: **reject** custom types for v1. Keep the door open for a future
`cfncompat.AmiIdType` on an *input* argument if we ever accept AMI ids as arguments.

### 1d. `ObjectAttribute` / `SingleNestedAttribute`

- `FW/datasource/schema/object_attribute.go` has `AttributeTypes map[string]attr.Type` +
  `CustomType basetypes.ObjectTypable`; `single_nested_attribute.go` has nested `Attributes`.

An object like `{ string = ..., list = [...] }` is statically typed, but it just moves the
"one of these two is null" problem one level down while making every consumer write
`.value.string`. The generator gets a struct type it must destructure. No gain over §1a.

Verdict: reject.

### Recommendation per CFN parameter type

| CFN/SSM type | Terraform shape | Attribute |
|---|---|---|
| (a) `String` | `string` | `value` — `schema.StringAttribute{Computed: true, Sensitive: true}` |
| (b) `StringList` (`List<String>`) | `list(string)` | `values` — `schema.ListAttribute{ElementType: types.StringType, Computed: true, Sensitive: true}`, split provider-side on `,` |
| (c) `SecureString` | `string`, decrypted | `value` (always sensitive) + `insecure_value` null |

**Comma splitting:** use `strings.Split(raw, ",")` and *do not* trim or drop empties — this matches
the provider's own `Fn::Split` contract, which documents that consecutive/leading/trailing
delimiters yield empty entries (`P/internal/provider/function_split.go:40-41`). CFN's
`CommaDelimitedList` does trim surrounding whitespace per element; that difference must be an
explicit, documented decision, not an accident. Recommend a `trim_elements` bool argument
(default `true`, matching CFN) rather than baking one behaviour in. Note the degenerate case:
`Split(",", "")` returns `[""]`, not `[]` — CFN's `CommaDelimitedList` of an empty string is
likewise a one-element list containing the empty string, so plain `strings.Split` is correct and
must be covered by a test.

---

## 2. Sensitivity

**Sensitivity is static, per-attribute, schema-level. There is no per-value sensitive marking in
the protocol.**

- Framework: `Sensitive bool` is a plain schema struct field on every attribute type
  (`FW/datasource/schema/string_attribute.go:57-62`, `list_attribute.go:76-81`,
  `dynamic_attribute.go:58-63`), surfaced only through `IsSensitive()` (`string_attribute.go:180-182`).
- Protocol: `PG/tfprotov6/schema.go:243-248` — `Sensitive bool` lives on `SchemaAttribute`, and
  the doc is explicit: *"This does not encrypt or otherwise protect these values in state, it only
  offers protection from them showing up in plans or other output."*
- `PG/tfprotov6/data_source.go:98-116` — `ReadDataSourceResponse` carries only `State`,
  `Diagnostics`, `Deferred`. **No place to attach a per-value sensitivity mark.** A provider
  cannot decide at read time that *this particular* value is sensitive.

Consequences:

- A single `value` attribute must be **always sensitive** or **never**. `hashicorp/aws` chose
  always-sensitive, and added a second non-sensitive attribute. Confirmed on disk from the
  generated binding `/Users/vincentsmet/cdktn/ref-provider-aws/src/data-aws-ssm-parameter/index.ts`:
  computed `arn`, `insecureValue` (:121), `type` (:154-156), `value` (:160), `version` as a
  **number** (:164-166); inputs `name` (required, :25), `withDecryption` (optional bool, :35),
  `region` (:31).
- **Conditionally-null computed attributes are fully supported** (see §1a), so
  `insecure_value` = null when the parameter is a `SecureString` is legal and is what AWS does.
- Terraform core's `sensitive()` function and `nonsensitive()` are the practitioner-side escape
  hatches; they are core features, nothing the provider participates in.

### Ephemeral resources — present in v1.19.0

- `FW/ephemeral/` exists: `ephemeral_resource.go:27-38` (`EphemeralResource` = `Metadata` /
  `Schema` / `Open`), `:44-53` `WithRenew`, `:58-64` `WithClose`, `:70-76` `WithConfigure`
  (so an ephemeral resource **does** get provider data / AWS clients, unlike a function).
- `FW/ephemeral/doc.go:7-16` — *"ephemeral values, which will not be stored in any artifact
  produced by Terraform (plan/state)… can only be referenced in other ephemeral values."*
- `FW/ephemeral/open.go:29-37` `OpenRequest{Config, ClientCapabilities}`, `:43-80`
  `OpenResponse{Result, Private, RenewAt, Diagnostics, Deferred}`.
- **Terraform 1.10+ required**: `FW/provider/provider.go:95` — *"Ephemeral resources are supported
  in Terraform version 1.10 and later."* Interface at `:96-104`.
- The provider already declares `provider.ProviderWithEphemeralResources`
  (`P/internal/provider/provider.go:24-25`) and returns `nil` today (`:314-317` region).

Is `cfncompat_ssm_secure_parameter_value` better as an ephemeral resource? **Yes in principle,
no for v1.** The hard blocker is the "can only be referenced in other ephemeral values" rule: an
ephemeral value can only flow into provider config, other ephemeral resources, or a resource's
**write-only** argument. Our consumer feeds these values into **awscc** resource attributes, and
awscc has **no write-only attributes at all** (`grep -rl 'writeOnly\|WriteOnly'
/Users/vincentsmet/cdktn/cdktn-awscc/generated/` → no hits). So an ephemeral SSM value would have
nowhere legal to go. Note `hashicorp/aws` does ship both:
`/Users/vincentsmet/cdktn/ref-provider-aws/src/ephemeral-aws-ssm-parameter/index.ts` — takes `arn`
(required, :18) and exposes `name`/`type`/`value`/`version`/`with_decryption`, and notably **no
`insecure_value`** (nothing to protect against when nothing is persisted).

Recommendation: ship the data source now; file a follow-up for
`ephemeral "cfncompat_ssm_parameter_value"` as a strict superset-of-safety sibling, to be enabled
when a write-only consumer exists.

### Write-only arguments

- `FW/resource/schema/string_attribute.go:156-164` — `WriteOnly bool`; *"Terraform will not store
  this attribute value in the plan or state artifacts. If WriteOnly is true, either Optional or
  Required must also be true. WriteOnly cannot be set with Computed."*, and `:161` *"only supported
  in Terraform 1.11 and later."*
- **WriteOnly is a resource-only concept** — there is no `WriteOnly` field in
  `datasource/schema/` or `ephemeral/schema/` (grep confirms: only `Sensitive` there, e.g.
  `FW/ephemeral/schema/string_attribute.go:57-60`).
- `hashicorp/aws` `aws_ssm_parameter` already has the pattern:
  `ref-provider-aws/src/ssm-parameter/index.ts:83 valueWo`, `:87 valueWoVersion`,
  `:229 hasValueWo` — the classic `_wo` + `_wo_version` triple.
- awscc: none. So CFN's `{{resolve:ssm-secure:…}}` (only legal in password-type properties)
  currently has **no** write-only landing zone on the awscc side. This is a gap to raise with the
  CFN-side study, not something this data source can fix.

---

## 3. Provider-defined functions cannot do I/O

- `FW/function/function.go:14-26` — the `Function` interface is exactly `Metadata`, `Definition`,
  `Run`. `:13` — *"Provider-defined functions are supported in Terraform version 1.8 and later."*
- `FW/function/run.go:9-13` — `RunRequest{ Arguments ArgumentsData }`. **That is the entire
  request.** No `ProviderData`, no client, no context beyond `context.Context`.
- `grep -rn "Configure" FW/function/*.go` (excluding tests) returns **nothing** — there is no
  `FunctionWithConfigure` interface in v1.19.0. Confirmed.
- `FW/function/run.go:17-27` — `RunResponse{ Error *FuncError, Result ResultData }`. Functions
  return **errors only** (`FuncError`), not warning diagnostics, and they cannot return unknown:
  Terraform short-circuits a call with any unknown argument and never invokes `Run`.
- Dynamic in functions is available: `FW/function/dynamic_parameter.go`,
  `FW/function/dynamic_return.go` exist; `FW/function/object_return.go:28-40`
  (`AttributeTypes map[string]attr.Type` + `CustomType basetypes.ObjectTypable`).

**Conclusion.** `cfncompat_resolve_dynamic_reference("{{resolve:ssm:/foo:3}}")` as a *function*
is impossible — it needs an SSM `GetParameter` call. Split the concern:

- `provider::cfncompat::parse_dynamic_reference(str)` → **pure function**, `function.ObjectReturn`
  with `AttributeTypes {service, name, version, label, key}`. Statically typed, no I/O, testable
  offline. Perfectly matches the 17 existing intrinsic functions' shape
  (`P/internal/provider/function_split.go:33-58` is the template).
- `data "cfncompat_ssm_parameter_value"` → does the I/O.
- **Substring** dynamic references (a `{{resolve:…}}` embedded inside a larger string) must be
  expanded by the **synth backend**: it parses the template string, emits one data source per
  distinct reference, and rebuilds the string with HCL interpolation. The provider should not ship
  a "expand every reference in this blob" surface — it would need N variadic reads and would make
  the whole result one opaque sensitive string.

---

## 4. Data source plan-time semantics

- `FW/datasource/read.go:27-41` — `ReadRequest{Config, ProviderMeta, ClientCapabilities}`;
  `:29-32` *"This configuration may contain unknown values if a user uses interpolation… that
  would prevent Terraform from knowing the value at request time."* This is the framework
  acknowledging the two regimes: all-known config → Terraform reads the data source **during plan,
  every plan**; any unknown in config → the whole data source result is unknown at plan and the
  read happens at apply.
- `FW/datasource/read.go:14-21` — `ReadClientCapabilities{DeferralAllowed bool}`.
- `FW/datasource/read.go:57-65` — `ReadResponse.Deferred *Deferred`, *"can only be set if
  ClientCapabilities.DeferralAllowed is true"* and *"related to deferred action support, which is
  currently experimental and is subject to change or break without warning. It is not protected by
  version compatibility guarantees."*
- `FW/datasource/deferred.go:6-21` — reasons `DataSourceConfigUnknown`, `ProviderConfigUnknown`,
  `AbsentPrereq`; `:27-30` the `Deferred` struct.

**Version drift.** With no `version`/`label` argument we resolve "latest", and a plan and its
apply can straddle a parameter update — Terraform will report a post-apply inconsistency or
silently use the newer value. Two mitigations, both needed:

1. Expose a computed `version` (number) output so a user/generator can read what was actually
   resolved. AWS does this (`ref-provider-aws/src/data-aws-ssm-parameter/index.ts:164-166`).
2. Accept an optional `version` (int64) and `label` (string) input so the generator can pin, and
   document that omitting both means "latest, resolved at read time".

**Deferred:** do **not** use it. It is explicitly experimental and unversioned, needs
`-allow-deferral`, and our unknown-config case is already handled by Terraform's normal
defer-to-apply behaviour.

---

## 5. Validation and plan modifiers

- `FW/datasource/data_source.go:39` `DataSourceWithConfigure`, `:56`
  `DataSourceWithConfigValidators`, `:72` `DataSourceWithValidateConfig` — **all three exist for
  data sources.**
- `FW/datasource/config_validator.go:9-27` — `ConfigValidator{Description, MarkdownDescription,
  ValidateDataSource}`; note `:24-25` the method is named `ValidateDataSource` (not
  `ValidateResource`) *"in order to allow generic validators"* — i.e. `datasourcevalidator.*`
  helpers plug straight in.
- `FW/datasource/validate_config.go:15-33` — `ValidateConfigRequest{Config}` /
  `ValidateConfigResponse{Diagnostics}`.
- **`terraform-plugin-framework-validators` is NOT a dependency** of this provider (absent from
  `P/go.mod`, and there is no `terraform-plugin-framework-validators` directory in
  `/Users/vincentsmet/go/pkg/mod/github.com/hashicorp/`). The provider **hand-rolls** its
  validators: `P/internal/provider/data_source_pseudo_parameters.go:394-410`
  (`pseudoParametersStackNameValidator` implementing `validator.String` with
  `Description`/`MarkdownDescription`/`ValidateString`, and correctly short-circuiting on
  `IsNull() || IsUnknown()`), wired at `:124` as `Validators: []validator.String{…}`.
  → **Follow that convention**: hand-roll a `ssmParameterTypeValidator` (one-of
  `String|StringList|SecureString`) rather than adding the validators module for one `OneOf`.
  Adding `terraform-plugin-framework-validators` is a defensible alternative but is a new
  dependency and a deviation; raise it explicitly if chosen.
- **`ConfigValidators` for `version` ⨯ `label` mutual exclusion**: since the validators module is
  absent, `datasourcevalidator.Conflicting` is unavailable — implement
  `DataSourceWithValidateConfig` (`FW/datasource/data_source.go:72`) with a hand-written check, or
  add the module. This is the one place the module would earn its keep.
- **Plan modifiers do not exist for data sources** — `FW/datasource/schema/` attributes have
  `Validators` but no `PlanModifiers` field (compare `FW/resource/schema/`). Nothing to do.
- **Timeouts**: `terraform-plugin-framework-timeouts` is likewise **not** in `P/go.mod` nor in the
  module cache. It does support data sources (`timeouts.Attributes(ctx)` → a `read` timeout), but
  a single `GetParameter` call does not warrant a new dependency; rely on the AWS SDK's retryer
  configured on `ProviderData.AwsConfig`.

---

## 6. Provider configuration access

Established pattern, to copy verbatim:

- `P/internal/provider/provider_config.go:109-128` — `ProviderData{AwsConfig aws.Config;
  ConfigErr error; …; Endpoints EndpointsConfig}`. `:114-120`: a failed AWS config resolution is
  **not** a Configure-time error — it is parked on `ConfigErr` so that a config which never touches
  an AWS-needing node still works.
- `P/internal/provider/data_source_pseudo_parameters.go:214-244` — `Configure`: return early on
  `req.ProviderData == nil`; type-assert to `*ProviderData` and `AddError` with
  *"This is a bug in the cfncompat provider; please report it."* on mismatch; stash `d.providerData`;
  return early if `pd.ConfigErr != nil`; then build the service client honouring the endpoint
  override (`:239-243`, `sts.NewFromConfig(pd.AwsConfig, func(o *sts.Options){ o.BaseEndpoint = … })`).
- `Read` re-checks `d.providerData == nil` (`:253-259`) and surfaces `ConfigErr` there
  (`data_source_availability_zones.go:238-243`).
- **Narrow client interfaces** so tests need no AWS:
  `data_source_pseudo_parameters.go:50-52` `callerIdentityGetter`,
  `data_source_availability_zones.go:58` `availabilityZonesAPI`. `P/CLAUDE.md` makes this a
  mandated convention.

**New dependency required.** `P/go.mod` has `service/ec2`, `service/lambda`, `service/s3`,
`service/sns`, `service/sts` — **no `service/ssm`**. Adding
`github.com/aws/aws-sdk-go-v2/service/ssm` is unavoidable. Also add an `ssm` field to
`providerEndpointsModel` / `EndpointsConfig` (`provider_config.go:72-107`) for parity with the
existing five, and to the provider schema.

---

## 7. Testing patterns

- **Unit**, no Terraform binary, no AWS: `P/internal/provider/data_source_shared_test.go:20-60`
  gives `dataSourceSchema`, `dataSourceConfig`, `dataSourceStateFromConfig` and the generic
  `readDataSource[M any](t, d, config) (M, *datasource.ReadResponse)`. Every logic branch is tested
  through a **fake client implementing the narrow interface** — see
  `data_source_availability_zones_test.go` (`TestResolveAvailabilityZones*`,
  `TestAvailabilityZonesDataSourceRead*`, `TestAvailabilityZonesDataSourceConfigure` at `:767`).
  No `httptest`, no mocking library.
- **Acceptance**, real Terraform CLI, real AWS, double-gated:
  `data_source_availability_zones_test.go:870-877` —
  `if os.Getenv("CFNCOMPAT_TEST_AWS") != "1" { t.Skip(...) }` then `rtresource.Test` with
  `ProtoV6ProviderFactories: testAccProtoV6ProviderFactories` (defined in `provider_test.go`).
  Checks use `TestCheckResourceAttrSet` / `TestMatchResourceAttr` / `TestCheckResourceAttrPair`,
  never hardcoded account-specific values. Note the import alias `rtresource` for
  `terraform-plugin-testing/helper/resource` (the linter bans `terraform-plugin-sdk/v2` entirely —
  `P/CLAUDE.md` Conventions).
- **E2E** terratest under `P/integ/`, gated by `CFNCOMPAT_E2E_AWS=1`.
- For SSM the acceptance test needs a parameter to exist; either create one in a `TestStep`
  prelude via `aws_ssm_parameter` (not available — no aws provider) or require the operator to set
  `CFNCOMPAT_TEST_SSM_PARAMETER=/some/name`. Recommend the latter, matching
  `CFNCOMPAT_TEST_LAMBDA_ARN` / `CFNCOMPAT_TEST_RESPONSE_BUCKET`.

---

## Comparison table

Scores: ++ excellent, + good, ~ acceptable, − poor, −− disqualifying.

| Strategy | Plan-time type known | Sensitivity handling | Generator ergonomics | FW maturity | TF core required |
|---|---|---|---|---|---|
| **A. Two computed attrs (`value` str + `values` list) in one DS** | **++** both types static, survive unknown | ~ whole DS is always-sensitive, or add `insecure_value` | **++** two typed getters, no casts, one node | ++ v0.x-era, universally used | 1.0 |
| B. `DynamicAttribute` for `value` | −− `DynamicPseudoType` when unknown; kills `for_each`/`length` | ~ same static flag | −− binding is `any`/`IResolvable` | + since v1.7 | 1.0 (dynamic wire type is old) |
| C. Custom `StringTypable` (CommaDelimitedList / AMI-id) | + wire type still `string` | ~ unchanged | − pretends to be a list, still `string` to the generator | + but `TypeWithValidate` deprecated → `ValidateableAttribute` | 1.0 |
| D. `ObjectAttribute`/`SingleNestedAttribute` result | ++ static | ~ per-nested-attr flags | ~ forces `.value.string` destructuring | ++ | 1.0 |
| E. Separate data sources per SSM type (`…_string` / `…_string_list` / `…_secure`) | ++ static, each single-purpose | **++** sensitivity is per-data-source, exactly right | + N generated classes, but each one honest; generator must know the type at synth (it does — CFN declares it) | ++ | 1.0 |
| F. Ephemeral resource for SecureString | ++ static | **++** never enters plan or state | −− result can only flow into write-only args; **awscc has none** | + since FW v1.10 | **1.10+** |
| G. Write-only args on the consuming resource | n/a | ++ | −− awscc emits no `_wo` attributes | + since FW v1.14 | **1.11+** |

**Recommendation: A + E hybrid.** One data source with both `value` and `values` (A) for the
common path, and separate `insecure_value`/`insecure_values` companions to solve sensitivity the
way `hashicorp/aws` does. Do **not** split into three data sources for v1 — the CFN
`AWS::SSM::Parameter::Value<T>` surface already parameterises the *inner* type across a single
concept, and one node keeps the generator's mapping 1:1 with the CFN parameter.

---

## Recommended schema sketch (Go)

```go
// datasource/schema, package-aliased `schema` per P/internal/provider convention.
resp.Schema = schema.Schema{
    Description:         "...",
    MarkdownDescription: "...",
    Attributes: map[string]schema.Attribute{
        // ---- inputs ----
        "name": schema.StringAttribute{
            Required:   true,
            Validators: []validator.String{ssmParameterNameValidator{}}, // non-empty, /-rooted or bare
        },
        "version": schema.Int64Attribute{ // mutually exclusive with `label`
            Optional: true,
        },
        "label": schema.StringAttribute{
            Optional: true,
        },
        "with_decryption": schema.BoolAttribute{ // default true, applied in Read
            Optional: true,
            Computed: true,
        },
        "trim_elements": schema.BoolAttribute{ // CFN CommaDelimitedList trims; default true
            Optional: true,
            Computed: true,
        },

        // ---- outputs ----
        "arn":  schema.StringAttribute{Computed: true},
        "type": schema.StringAttribute{Computed: true}, // "String"|"StringList"|"SecureString"
        "resolved_version": schema.Int64Attribute{Computed: true}, // pin-back for drift

        "value": schema.StringAttribute{
            Computed:  true,
            Sensitive: true, // static: SecureString forces this for the whole attribute
        },
        "values": schema.ListAttribute{
            ElementType: types.StringType,
            Computed:    true,
            Sensitive:   true,
            // null unless type == "StringList"; comma-split of `value`
        },
        "insecure_value": schema.StringAttribute{
            Computed: true, // null when type == "SecureString"
        },
        "insecure_values": schema.ListAttribute{
            ElementType: types.StringType,
            Computed:    true, // null unless type == "StringList"
        },

        "id": schema.StringAttribute{Computed: true}, // convention: see pseudo_parameters ":203"
    },
}
```

Model struct (`tfsdk` tags, mirroring `PseudoParametersDataSourceModel` at
`data_source_pseudo_parameters.go:76-85`):

```go
type SsmParameterValueDataSourceModel struct {
    Name            types.String `tfsdk:"name"`
    Version         types.Int64  `tfsdk:"version"`
    Label           types.String `tfsdk:"label"`
    WithDecryption  types.Bool   `tfsdk:"with_decryption"`
    TrimElements    types.Bool   `tfsdk:"trim_elements"`
    Arn             types.String `tfsdk:"arn"`
    Type            types.String `tfsdk:"type"`
    ResolvedVersion types.Int64  `tfsdk:"resolved_version"`
    Value           types.String `tfsdk:"value"`
    Values          types.List   `tfsdk:"values"`
    InsecureValue   types.String `tfsdk:"insecure_value"`
    InsecureValues  types.List   `tfsdk:"insecure_values"`
    Id              types.String `tfsdk:"id"`
}
```

Narrow client interface, in the same file:

```go
type ssmParameterGetter interface {
    GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}
```

Cross-attribute rule (`version` ⨯ `label`) via `datasource.DataSourceWithValidateConfig`
(`FW/datasource/data_source.go:72`), hand-written to avoid the validators dependency.

**Alternatives, one line each.** (B) replace `value`/`values` with
`schema.DynamicAttribute{Computed: true, Sensitive: true}` and set
`types.DynamicValue(types.StringValue(...))` or `types.DynamicValue(listVal)` in `Read` — rejected,
see §1b. (D) collapse the four value attributes into
`schema.SingleNestedAttribute{Computed: true, Attributes: {"string": …, "list": …}}` — rejected,
same nullability, worse ergonomics. (E) three data sources `_string`/`_string_list`/`_secure`,
each with exactly one non-null value attribute and per-source `Sensitive` — the cleanest
sensitivity story; keep as the fallback if the always-sensitive `value` proves unworkable.
(F) `ephemeral "cfncompat_ssm_parameter_value"` with `Open` doing the `GetParameter` and
`EphemeralResourceWithConfigure` supplying the client — follow-up, blocked on write-only consumers.

---

## Conventions checklist (from existing provider code)

1. One file per surface: `internal/provider/data_source_ssm_parameter_value.go` +
   `_test.go` beside it (`P/CLAUDE.md` Architecture).
2. Register in `P/internal/provider/provider.go:320-326` `DataSources()`.
3. `// Copyright (c) 2026 cdktn-io` / `// SPDX-License-Identifier: MPL-2.0` header
   (inserted by `make generate` / copywrite).
4. Interface assertions at top of file:
   `var _ datasource.DataSource = &X{}` and `var _ datasource.DataSourceWithConfigure = &X{}`
   (`data_source_pseudo_parameters.go:26-27`).
5. AWS access through a **narrow, fakeable interface** declared in the same file.
6. `Configure`: nil `ProviderData` → return; bad type → `AddError(..., "This is a bug in the
   cfncompat provider; please report it.")`; `ConfigErr != nil` → return without building a client.
   Surface `ConfigErr` from `Read`, not `Configure`.
7. Honour `pd.Endpoints.<Service>` via `o.BaseEndpoint` when non-empty; add an `ssm` endpoint.
8. Both `Description` (plain) and `MarkdownDescription` (with doc links) on the schema and on
   **every** attribute — these feed `tfplugindocs`.
9. Hand-rolled validators implementing `validator.String` with `Description` /
   `MarkdownDescription` / `ValidateString`, short-circuiting on `IsNull() || IsUnknown()`.
10. A computed `id` attribute (all existing data sources have one).
11. `examples/data-sources/cfncompat_ssm_parameter_value/data-source.tf` — the filename is fixed
    by tfplugindocs — then `make generate` and commit `docs/data-sources/ssm_parameter_value.md`;
    CI fails on a `make generate` diff.
12. Never import `terraform-plugin-sdk/v2` (golangci-lint denies it).
13. Write an RFC in `RFCs/` matching the style of `006-pseudo-parameter-polyfill.md`
    (status table, "1. Decision" with a rejected-alternatives paragraph, attribute-contract table,
    HCL sample, testing section).

---

## Answers to the questions the CFN-side study will raise

- **`CommaDelimitedList` splitting.** Provider-side `strings.Split(raw, ",")`, no empties dropped
  (matches `Fn::Split`, `function_split.go:40-41`). CFN trims whitespace around each element of a
  `CommaDelimitedList`; expose that as `trim_elements` (default `true`). `Split(",", "")` →
  `[""]`, which is also CFN's behaviour — test it.
- **`List<AWS::EC2::Subnet::Id>` and friends.** All typed inner lists are `list(string)` on the
  Terraform side. The CFN inner type is a *validation* concern, not a *typing* concern — Terraform
  has no `subnet-id` scalar type. Two options: (i) ignore, let the consuming awscc resource reject
  a bad id at apply; (ii) accept an optional `value_type` argument (`"AWS::EC2::Image::Id"`,
  `"List<AWS::EC2::Subnet::Id>"`, `"CommaDelimitedList"`, …) validated by a hand-rolled one-of
  validator, and have `Read` regex-check each element and raise an error diagnostic on mismatch.
  Recommend (ii): it makes the data source a faithful `AWS::SSM::Parameter::Value<T>` and gives
  the generator a place to put the CFN type it already knows. It also derives whether to populate
  `value` or `values` without depending on SSM's own `Type`.
- **AMI-id validation.** `^ami-([0-9a-f]{8}|[0-9a-f]{17})$`, applied in `Read` against the resolved
  value when `value_type == "AWS::EC2::Image::Id"`. Do **not** build a custom `attr.Type` for it:
  `xattr.TypeWithValidate` is deprecated (`FW/attr/xattr/type.go:16-30`), and the modern
  `xattr.ValidateableAttribute` (`FW/attr/xattr/attribute.go:13-21`) validates values coming *from*
  Terraform, which a computed output never is.
- **Version pinning.** Optional `version` (int64) and `label` (string) inputs, mutually exclusive
  via `ValidateConfig`; computed `resolved_version` always reports what was actually read. Without
  a pin the data source re-reads every plan and can drift between plan and apply — document this.
- **Sensitivity.** Static per attribute, no per-value marking anywhere in the protocol
  (`PG/tfprotov6/schema.go:243-248`, `PG/tfprotov6/data_source.go:98-116`). `value`/`values` are
  always sensitive; `insecure_value`/`insecure_values` are never sensitive and are null when the
  parameter is a `SecureString`. Conditionally-null computed attributes are fully supported.
- **Substring dynamic references.** The provider parses (pure function
  `parse_dynamic_reference` → `function.ObjectReturn`) and resolves (data source). **Reassembling**
  a string that embeds `{{resolve:ssm:…}}` inside other text is the synth backend's job: emit one
  data source per distinct reference and rebuild with interpolation. `{{resolve:ssm-secure:…}}`
  additionally needs a write-only sink on the consuming resource, which awscc does not currently
  provide — flag this as a cross-cutting gap.
