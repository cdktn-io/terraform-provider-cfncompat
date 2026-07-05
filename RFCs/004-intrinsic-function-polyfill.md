# RFC 004: Intrinsic-Function Polyfill Surface (provider-defined functions)

|  |  |
|---|---|
| **Status:** | Implemented |
| **Companion:** | `000-PRD.md` (§5 gap G3), `002-architecture.md` (§ `TerraformIntrinsicResolver`) |

## 1. Decision

Every CloudFormation intrinsic function that is a **pure computation** — no AWS API access, no template/synthesis/deployment context — is implemented in this provider as a Terraform **provider-defined function** (`provider::cfncompat::*`), each matching AWS-documented semantics exactly and carrying unit + acceptance tests derived from the AWS documentation examples.

This **supersedes part of RFC 002's `TerraformIntrinsicResolver` mapping table**: RFC 002 proposed mapping most `Fn.*` calls to Terraform core functions (`Fn::Join`→`join()`, `Fn::Select`→`element()`, `Fn::Base64`→`base64encode()`, …). The implemented design instead routes **all** intrinsic-function calls to `cfncompat` provider functions, for three reasons:

1. **Semantic fidelity.** TF core functions are near-misses, not equivalents (e.g. `cidrsubnets()` computes different subnet layouts than `Fn::Cidr`; `element()` wraps around on out-of-range indexes where `Fn::Select` must fail; TF `split()` on an empty source returns `[""]`-shaped edge cases that must match CFN, not HCL, behavior). One implementation, tested against AWS docs, removes an entire class of translation bugs.
2. **Uniform rendering.** The synthesis backend emits one call shape (`provider::cfncompat::<name>(...)`) for every `Fn.*`, instead of maintaining a per-function mapping with per-function edge-case shims.
3. **Single polyfill provider.** RFC 002 already requires the provider for token-valued `Fn::Cidr` and the custom-resource protocol; carrying the whole function surface adds no new dependency.

## 2. Function inventory and naming

Naming rule: the Terraform function name is the **snake_case of the AWS CDK `Fn` method name** (not the raw CFN name). The CDKTN provider-binding generator emits `public static <toCamelCase(tf_name)>(...)`, so this makes the generated TypeScript/JSII API line up 1:1 with AWS CDK's `Fn` class.

| Terraform function | CFN intrinsic | CDK `Fn` method | Generated CDKTN binding |
|---|---|---|---|
| `base64` | `Fn::Base64` | `base64` | `base64` |
| `cidr` | `Fn::Cidr` | `cidr` | `cidr` |
| `find_in_map` | `Fn::FindInMap` | `findInMap` | `findInMap` |
| `join` | `Fn::Join` | `join` | `join` |
| `length` | `Fn::Length` ⁽ᴸᴱ⁾ | `len` | `lengthOf` (see §3) |
| `select` | `Fn::Select` | `select` | `select` |
| `split` | `Fn::Split` | `split` | `split` |
| `sub` | `Fn::Sub` | `sub` | `sub` |
| `to_json_string` | `Fn::ToJsonString` ⁽ᴸᴱ⁾ | `toJsonString` | `toJsonString` |
| `condition_and` | `Fn::And` | `conditionAnd` | `conditionAnd` |
| `condition_contains` | `Fn::Contains` ⁽ᴿ⁾ | `conditionContains` | `conditionContains` |
| `condition_each_member_equals` | `Fn::EachMemberEquals` ⁽ᴿ⁾ | `conditionEachMemberEquals` | `conditionEachMemberEquals` |
| `condition_each_member_in` | `Fn::EachMemberIn` ⁽ᴿ⁾ | `conditionEachMemberIn` | `conditionEachMemberIn` |
| `condition_equals` | `Fn::Equals` | `conditionEquals` | `conditionEquals` |
| `condition_if` | `Fn::If` | `conditionIf` | `conditionIf` |
| `condition_not` | `Fn::Not` | `conditionNot` | `conditionNot` |
| `condition_or` | `Fn::Or` | `conditionOr` | `conditionOr` |

⁽ᴸᴱ⁾ requires the `AWS::LanguageExtensions` transform in CFN templates (a no-op concern here; the synthesis backend suppresses `addTransform` via `requireCapability`, per RFC 002). ⁽ᴿ⁾ CFN rule functions (Rules section).

### Why `condition_*` instead of bare `if`/`and`/`or`/`not`

The CDKTN provider-function binding generator (`packages/@cdktn/provider-generator/src/get/generator/models/provider-function-model.ts`) transforms names with plain `toCamelCase()` and has **no reserved-word escaping** for method names (`sanitizeMethodName`, line 65–68; the only special case is `length` → `lengthOf`, line 66). jsii's own reserved-word diagnostics are silenced during generation (`constructs-maker.ts:98`, `--silence-warnings reserved-word`). A Terraform function named `if` would therefore emit `public static if(...)` — broken TypeScript — and `and`/`or`/`not` are keywords in JSII target languages (Python, C#). AWS CDK's own `Fn` class solved the identical problem the same way: `Fn.conditionAnd`, `Fn.conditionIf`, etc. Adopting the `condition_` prefix means **no rename map is needed anywhere** — mechanical snake→camel is exact.

## 3. Impact on the CDK Terrain synthesis plugin (`TerraformIntrinsicResolver`)

Consequences the AWSCDK synthesis-backend implementation must account for:

1. **One rename exception.** `Fn::Length` is CDK `Fn.len()` (JS forbids a static class member named `length`), and the CDKTN generator emits `lengthOf` for the TF name `length`. The resolver's `Fn.len` → `provider::cfncompat::length` mapping is therefore the single non-mechanical entry; everything else is snake_case(CDK method).
2. **Conditions are evaluated values, not names.** CFN's `Fn::If`/`Fn::And`/`Fn::Or`/`Fn::Not` reference or nest **condition expressions** from the template `Conditions` section, evaluated lazily by the CFN engine. The provider functions take **already-evaluated booleans**. The resolver must render a `CfnCondition` reference as the boolean-producing expression (which may itself be a `provider::cfncompat::condition_*` call tree) before embedding it.
3. **`Fn::FindInMap` takes the mapping inline.** There is no Mappings section in Terraform; the resolver passes the mapping object itself as argument 1 (RFC 002 already planned `m[k1][k2]` inlining for the synth-known case; the provider function is the general form and supports `DefaultValue` as a variadic 4th argument).
4. **`Fn::Sub` takes an explicit variable map.** `${Name}` placeholders that CFN would resolve as Ref/GetAtt/pseudo-params must be pre-resolved by the reference resolver into entries of the `variables` map argument (or concatenated directly); unresolvable placeholders are a provider-function error by design. `${!Literal}` escaping is implemented per docs.
5. **LanguageExtensions functions are pure here.** `to_json_string` and `length` need no transform registration; `requireCapability('AWS::LanguageExtensions', …)` remains a no-op on the TF target. Note `Fn.toJsonString()` is exported API but consumed by no L2/L3 construct in `aws-cdk-lib` (only unit-tested) — implemented for consumer-app pass-through completeness.
6. **Excluded intrinsics stay synthesis-owned.** Not provider functions, because they are impure or template-scoped:

   | Intrinsic | Disposition |
   |---|---|
   | `Ref` / `Fn::GetAtt` | Reference resolver (RFC 002 keystone, I1) — renders native TF interpolation |
   | `Fn::GetAZs` | AWS API — map to an `aws_availability_zones`-style data source at synth time |
   | `Fn::ImportValue` | CFN-only for v1 (RFC 002 §G5 cross-stack model) |
   | `Fn::Transform` | Clear synth-time error (RFC 002) |
   | `Fn::ForEach` | Template-language transform, expanded at synth time |
   | `Fn::RefAll` / `Fn::ValueOf` / `Fn::ValueOfAll` | CFN parameter-rule context; no TF analog |

7. **Binding generation build.** Provider-function binding codegen exists only in CDK Terrain (upstream cdktf has none); generate `cfncompat` bindings with the local `cdk-terrain` checkout carrying the provider-functions fix (validated in `cdktn-provider-features-demo/examples/functions-only`).

## 4. Provider-function signatures (implemented)

| Function | Signature |
|---|---|
| `base64` | `(content string) -> string` |
| `cidr` | `(ip_block string, count number, cidr_bits number) -> list(string)` — IPv4 and IPv6 |
| `find_in_map` | `(mapping dynamic, top_level_key string, second_level_key string, [default_value dynamic]) -> dynamic` |
| `join` | `(delimiter string, values list(string)) -> string` |
| `length` | `(value dynamic list/tuple/set) -> number` |
| `select` | `(index number, objects dynamic list/tuple) -> dynamic` |
| `split` | `(delimiter string, source string) -> list(string)` |
| `sub` | `(template string, variables map(string)) -> string` |
| `to_json_string` | `(value dynamic) -> string` |
| `condition_and` / `condition_or` | `(conditions bool...; 2–10) -> bool` |
| `condition_not` | `(condition bool) -> bool` |
| `condition_equals` | `(value_1 dynamic, value_2 dynamic) -> bool` — canonical-string comparison of primitives |
| `condition_if` | `(condition bool, value_if_true dynamic, value_if_false dynamic) -> dynamic` |
| `condition_contains` | `(list_of_strings list(string), value string) -> bool` |
| `condition_each_member_equals` | `(list_of_strings list(string), value string) -> bool` |
| `condition_each_member_in` | `(strings_to_check list(string), strings_to_match list(string)) -> bool` |
