# CloudFormation SSM / Secrets Manager Parameter Semantics — Live AWS Test Results

Account: 694710432912 (tcons-vincent), region us-east-1. All resources cleaned up (verified at end).

## Summary Table

| Test | Question | Observed behaviour | Verdict |
|---|---|---|---|
| T1 | Does `AWS::SSM::Parameter::Value<T>` accept `name:version`? | `:1`→hello-v1, `:2`→hello-v2 both work; same in `Default`. `:99` (nonexistent) → clean `ValidationError` at CreateStack call. `:prod` (label) → **`InternalFailure`** (opaque, reproducible) | PASS (versions), FAIL/BUG (labels give opaque error, not "unsupported") |
| T2 | Do AllowedPattern/AllowedValues/Min/MaxLength apply to the NAME or the RESOLVED value? | Pattern matching resolved value only → rejected. Pattern matching the NAME only → accepted (stack succeeded). AllowedValues=[resolved value] → rejected. | **Constraints validate the raw NAME string you pass in, not the resolved value** |
| T3 | Where/when does typed inner validation (`AWS::EC2::Image::Id` etc.) fail? | Accepted by CreateStack call; fails later as a stack-level (not resource-level) `ROLLBACK_COMPLETE` with reason "Parameter validation failed: parameter value X for parameter name P does not exist" — before any resource event. Applies to bad-format AND well-formed-but-nonexistent AMI ids alike (CFN calls EC2 to check existence, not just regex). Same behavior for `Subnet::Id`. SecureString type is rejected synchronously ("types not supported by CloudFormation"). `AWS::SSM::Parameter::Name` also validates the name exists. | Fails at **stack-level pre-resource validation**, not resource create, not the CreateStack API call (except SecureString type, which fails synchronously) |
| T4 | List shapes: trimming, cross-type mismatches | `List<String>`/`CommaDelimitedList` CFN parameter type must match the **actual SSM parameter Type** (String vs StringList) or CreateStack is rejected synchronously with "Types ... incompatible" — content shape (commas) is irrelevant. `List<String>` on a StringList **trims whitespace** around elements ("a,b, c ,d" → "a,b,c,d"). `List<AWS::EC2::Image::Id>` validates every element's existence individually. | Strict type-compatibility check + list-element trimming confirmed |
| T5 | Does an unversioned SSM-typed Parameter re-resolve on update? | `UsePreviousValue=true` update, and change-set for the same, both **re-resolve** to the current SSM value (not "No updates to be performed"); change-set `Parameters[].ResolvedValue` reflects the fresh value even with an empty `Changes` list. Unrelated template change also re-resolves. | **Always re-resolved on every stack update**, regardless of no-op-ness |
| T6 | Does `{{resolve:ssm:...}}` re-resolve on update? | Identical template update, unrelated added resource, and unrelated property change on a *different* attribute of the *same* resource — **all three re-resolve** the dynamic reference and produce a genuine `UPDATE_COMPLETE`, verified via `get-parameter` on the created resource. `:1` (version) works; `:prod` (label) → clean `ValidationError` ("Incorrect format..."). StringList via `{{resolve:ssm:...}}` returns the **raw, untrimmed** string (spaces preserved) — unlike the typed-Parameter List path in T4. | Dynamic ssm refs are unconditionally re-resolved every deploy; labels not supported; no trimming |
| T7 | Dynamic refs in Parameters/Default and mid-string | Unversioned `{{resolve:ssm:...}}` in a Parameter `Default` → rejected ("should not contain ssm versionless resolver"); `:1` accepted. Mid-string, multiple-refs-in-one-string, and refs nested inside `Fn::Sub` all interpolate correctly. | Default requires a pinned version; interpolation fully supported |
| T8 | ssm-secure misuse | `{{resolve:ssm-secure:...}}` in a disallowed property (`AWS::SSM::Parameter/Value`) is rejected with "SSM Secure reference is not supported in: [...]" — **synchronously** when a version is given, but only **asynchronously** (stack rollback) when unversioned. `ssm-secure` pointing at a plain String param → same "not supported in" error (property-level check fires first). `ssm` (non-secure) pointing at a SecureString → "Non-secure ssm prefix was used for secure parameter". `ssm-secure` with a `:prod` label → rejected, same "Incorrect format" error as T6. | All rejections captured verbatim; sync-vs-async split by whether a version is present |
| T9 | Secrets Manager dynamic refs | `:SecretString:password`→p2 (current), `:...:AWSPREVIOUS`→p1. Plain-text secret with no suffix or `:SecretString` → whole string. Whole secret with no key → entire JSON string. ARN as secret-id works. `:missingkey` → resource-level `CREATE_FAILED`: "Could not find a value associated with JSONKey in SecretString". Key-lookup on non-JSON secret → resource-level `CREATE_FAILED`: "Could not parse SecretString JSON". Resolved secrets land as **plain text** in the created SSM parameter (no automatic protection). A literal `{{resolve:secretsmanager:...}}` placed directly as an Output **Value is NOT resolved** — the literal placeholder string is returned. After `put-secret-value`, an unrelated stack update did **not** re-resolve the existing resource (value stayed at old version) — only forcing an actual property change on that specific resource triggered re-resolution. | **Key difference from `ssm`/`ssm-secure`: secretsmanager refs resolve only when the owning resource is otherwise being updated, not on every deploy.** Dynamic refs are inert when used directly as Output values (only resolved inside resource properties). Errors surface as resource-level CREATE_FAILED, not stack-level. |
| T10 | Public (AWS-owned) SSM params | `{{resolve:ssm:/aws/service/...}}` and `AWS::SSM::Parameter::Value<AWS::EC2::Image::Id>` against the public AMI parameter both work identically to a private parameter. | PASS, no special-casing needed |

## Surprises worth flagging

1. **AllowedPattern/AllowedValues/MinLength/MaxLength on an SSM-typed Parameter validate the NAME you pass in, not the resolved SSM value.** This is easy to get backwards when building a Terraform-provider polyfill — a pattern like `^ami-` intended to guard the resolved AMI id will actually validate the *parameter name* string.
2. **A CFN parameter value like `name:prod` (SSM label) inside `AWS::SSM::Parameter::Value<...>` produces an opaque `InternalFailure`** ("reached max retries: 2: Unknown") rather than a clean validation error — labels are simply not supported there, but CFN's error handling for this case is broken/unhelpful. (Confirmed reproducible 3x.)
3. **List<String>/List<T> resolution trims embedded whitespace around StringList elements** ("a,b, c ,d" → "a,b,c,d"), but the same StringList resolved via `{{resolve:ssm:...}}` returns the raw untrimmed string. Two different resolution paths, two different normalization behaviors for the exact same underlying value.
4. **`{{resolve:secretsmanager:...}}` is fundamentally different from `{{resolve:ssm:...}}`/`{{resolve:ssm-secure:...}}` in re-resolution semantics.** SSM-flavoured dynamic references are force-re-resolved on *every* stack operation that touches the stack (even an unrelated resource addition or an unrelated property change on a *different* attribute of the same resource). Secrets Manager references are only re-resolved when CloudFormation independently decides that specific resource needs an update (i.e., some property actually changed) — an update that leaves the resource's rendered properties textually identical will silently keep serving the stale secret version.
5. **Dynamic references used directly as an Output `Value` are not resolved at all** — CloudFormation returns the literal `{{resolve:...}}` string verbatim. They only resolve when embedded in a resource property.
6. **CFN does check AMI/Subnet existence, not just ID syntax**, for both the typed-Parameter path and the `List<AWS::EC2::Image::Id>` path — a syntactically valid but nonexistent `ami-...` id fails identically to a garbage string, both as "parameter value X ... does not exist", at the stack level, before any resource is touched.
7. **Type-checking for `AWS::SSM::Parameter::Value<List<...>>` / `<CommaDelimitedList>` is strict on the underlying SSM parameter's declared Type** (String vs StringList) — content shape (commas present or absent) is irrelevant; a String parameter containing commas cannot satisfy a List-typed CFN Parameter and vice versa.
8. **Secrets Manager reference errors are resource-level `CREATE_FAILED`**, while every other failure mode tested (SSM typed-parameter validation, ssm-secure misuse) is a **stack-level** failure that occurs before any resource event — a meaningful distinction for how a Terraform-provider polyfill should surface these errors.

---

## Setup

SSM parameters created:
- `/cfncompat/livetest/str` (String): v1="hello-v1", v2="hello-v2" (current), label `prod`→v1
- `/cfncompat/livetest/list` (StringList): "a,b, c ,d"
- `/cfncompat/livetest/strcommas` (String): "x,y,z"
- `/cfncompat/livetest/secure` (SecureString): v1="s3cr3t-v1", v2="s3cr3t-v2" (current), label `prod`→v1
- `/cfncompat/livetest/badami` (String): "not-an-ami"
- `/cfncompat/livetest/fakeami` (String): "ami-0123456789abcdef0"
- `/cfncompat/livetest/goodami` (String): "ami-081b0a6eac00b4f53" (copied from `/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64`)
- `/cfncompat/livetest/amilist` (StringList): "ami-081b0a6eac00b4f53,ami-081b0a6eac00b4f53"
- `/cfncompat/livetest/amilistbad` (StringList): "ami-081b0a6eac00b4f53,ami-0123456789abcdef0"

Secrets:
- `cfncompat-livetest/json`: `{"username":"u1","password":"p1"}` → `put-secret-value` → `{"username":"u2","password":"p2"}` (AWSCURRENT), AWSPREVIOUS = u1/p1. ARN: `arn:aws:secretsmanager:us-east-1:694710432912:secret:cfncompat-livetest/json-CJm4iP`
- `cfncompat-livetest/plain`: `plaintext-secret`

---

## T1 — `name:version` in SSM-typed Parameter

Template (base, `templates/t1-base.json`):
```json
{
  "Parameters": { "P": { "Type": "AWS::SSM::Parameter::Value<String>" } },
  "Resources": { "Handle": { "Type": "AWS::CloudFormation::WaitConditionHandle" } },
  "Outputs": { "PValue": { "Value": { "Ref": "P" } } }
}
```

**T1a** `ParameterValue=/cfncompat/livetest/str:1`: `create-stack` → CREATE_COMPLETE.
`describe-stacks` → `Parameters[0]`: `{"ParameterKey":"P","ParameterValue":"/cfncompat/livetest/str:1","ResolvedValue":"hello-v1"}`. Output `PValue`="hello-v1". **PASS**

**T1b** `:2` → CREATE_COMPLETE, ResolvedValue="hello-v2", Output="hello-v2". **PASS**

**T1c** `:prod` (label) → repeated 3x, always:
```
aws: [ERROR]: An error occurred (InternalFailure) when calling the CreateStack operation (reached max retries: 2): Unknown
```
**FAIL / opaque bug** — labels not accepted, no clean validation message.

**T1d** `:99` (nonexistent version) → immediate, clean:
```
aws: [ERROR]: An error occurred (ValidationError) when calling the CreateStack operation: Unable to fetch parameters [/cfncompat/livetest/str:99] from parameter store for this account.
```
**FAIL as expected, clean ValidationError, synchronous.**

**T1e** Same value (`/cfncompat/livetest/str:1`) placed in the Parameter's `Default` instead of passed explicitly (`templates/t1-default1.json`): create-stack with no `--parameters` → CREATE_COMPLETE, ResolvedValue="hello-v1". **PASS — Default supports `name:version` identically to an explicit ParameterValue.**

---

## T2 — Constraints on SSM-typed Parameters (name vs resolved value)

**T2a** `AllowedPattern: "^hello-.*$"` (matches the *resolved* value "hello-v2", not the name) with `ParameterValue=/cfncompat/livetest/str:2`:
```
aws: [ERROR]: An error occurred (ValidationError) when calling the CreateStack operation: Parameter 'P' must match pattern ^hello-.*$
```
Rejected.

**T2b** `AllowedPattern: "^/cfncompat.*$"` (matches the *name*, not "hello-v2") with the same `ParameterValue`: `create-stack` succeeded, CREATE_COMPLETE, `ResolvedValue: "hello-v2"`.

→ **AllowedPattern validates the raw name string, not the resolved value.**

**T2c** `AllowedValues: ["hello-v2"]` (the resolved value) with the same ParameterValue:
```
aws: [ERROR]: An error occurred (ValidationError) when calling the CreateStack operation: Parameter 'P' must be one of AllowedValues
```
Rejected — consistent with T2a/b: AllowedValues checked the name `/cfncompat/livetest/str:2`, not "hello-v2".

**T2d** `MinLength: 3, MaxLength: 5` with the same ParameterValue (name is 25 chars, resolved value is 8 chars — both > 5):
```
aws: [ERROR]: An error occurred (ValidationError) when calling the CreateStack operation: Parameter 'P' must contain at most 5 characters
```
Non-discriminating by itself but consistent with the name being checked (and either way, both interpretations exceed 5 chars).

---

## T3 — Typed inner value validation

Templates: `templates/t3-imageid.json` (`AWS::SSM::Parameter::Value<AWS::EC2::Image::Id>`), `templates/t3-subnetid.json` (`<AWS::EC2::Subnet::Id>`), `templates/t3-name.json` (`AWS::SSM::Parameter::Name`).

**T3a** Image::Id → `badami` ("not-an-ami"): `create-stack` accepted synchronously, then `ROLLBACK_COMPLETE`. Full event trail for the stack (no resource-level events at all — the WaitConditionHandle was never touched):
```
CREATE_IN_PROGRESS  "User Initiated"
ROLLBACK_IN_PROGRESS "Parameter validation failed: parameter value not-an-ami for parameter name P does not exist. Rollback requested by user."
ROLLBACK_COMPLETE
```

**T3b** Image::Id → `goodami` (real current AL2023 AMI id): CREATE_COMPLETE, ResolvedValue = "ami-081b0a6eac00b4f53". **PASS**

**T3c** Image::Id → `fakeami` ("ami-0123456789abcdef0", syntactically valid, does not exist): ROLLBACK_COMPLETE, same stack-level reason: "parameter value ami-0123456789abcdef0 for parameter name P does not exist." → **CFN checks actual existence via EC2, not just ID-format regex.**

**T3d** Subnet::Id → `badami`: ROLLBACK_COMPLETE, same-shaped reason ("parameter value not-an-ami for parameter name P does not exist").

**T3e** `AWS::SSM::Parameter::Value<String>` → the SecureString parameter `secure`: rejected **synchronously** at the CreateStack call:
```
aws: [ERROR]: An error occurred (ValidationError) when calling the CreateStack operation: Parameters [/cfncompat/livetest/secure] referenced by template have types not supported by CloudFormation.
```

**T3f** `AWS::SSM::Parameter::Name` → a non-existent name (`/cfncompat/livetest/does-not-exist`): ROLLBACK_COMPLETE, "parameter value /cfncompat/livetest/does-not-exist for parameter name P does not exist." → **`AWS::SSM::Parameter::Name` DOES validate the name exists**, despite its name suggesting it's just a plain string capture.

---

## T4 — List shapes

**T4a** `List<String>` on `list` (StringList "a,b, c ,d") with Output `Fn::Join["|", Ref P]`: CREATE_COMPLETE.
`Parameters[0].ResolvedValue` = **"a,b,c,d"** (trimmed!). Output `PJoined` = "a|b|c|d".
Raw SSM value confirmed via `get-parameter` = "a,b, c ,d" (untrimmed at storage) → **trimming happens in CFN's typed-Parameter resolution, not in SSM storage.**

**T4b** `CommaDelimitedList` on `strcommas` (String type, value "x,y,z"): synchronous rejection:
```
aws: [ERROR]: An error occurred (ValidationError) when calling the CreateStack operation: Types for SSM parameters [/cfncompat/livetest/strcommas] defined in CFN template and SSM are incompatible
```

**T4c** `List<String>` on `strcommas`: same error, same message.

**T4d** `AWS::SSM::Parameter::Value<String>` on `list` (StringList type): same error, reversed direction:
```
aws: [ERROR]: An error occurred (ValidationError) when calling the CreateStack operation: Types for SSM parameters [/cfncompat/livetest/list] defined in CFN template and SSM are incompatible
```
→ **CFN strictly compares the declared SSM parameter Type (String vs StringList) against the CFN Parameter's shape (scalar vs List/CommaDelimitedList) — content is irrelevant.**

**T4e** `List<AWS::EC2::Image::Id>` on `amilist` (two copies of the real AMI id): CREATE_COMPLETE, ResolvedValue = "ami-081b0a6eac00b4f53,ami-081b0a6eac00b4f53", Output joined correctly.

**T4f** `List<AWS::EC2::Image::Id>` on `amilistbad` (real AMI + fake AMI): ROLLBACK_COMPLETE, reason: "parameter value ami-0123456789abcdef0 for parameter name P does not exist." → per-element existence validation for list types too.

---

## T5 — Re-resolution of SSM-typed Parameters on update

Stack `t5` created with unversioned `/cfncompat/livetest/str` (resolved "hello-v2" at creation time).

**T5a** Overwrote `str`→"hello-v3", then:
```
update-stack --use-previous-template --parameters ParameterKey=P,UsePreviousValue=true
```
→ accepted (not "No updates are to be performed"), `UPDATE_COMPLETE`, `ResolvedValue` = **"hello-v3"**.

**T5b** Overwrote `str`→"hello-v4", then `create-change-set --use-previous-template --parameters ParameterKey=P,UsePreviousValue=true --change-set-type UPDATE`:
`describe-change-set` → `Changes: []` (no resource diff) but `Parameters[0].ResolvedValue` = **"hello-v4"** — the change set reflects the fresh resolution even though there's nothing to actually change on any resource. Change set deleted without executing.

**T5c** Update with an unrelated template change (added an `Extra` Output), still `UsePreviousValue=true`: `UPDATE_COMPLETE`, `ResolvedValue` and Output `PValue` both = "hello-v4" (the real stack was still at "hello-v3" from T5a; this confirms it re-resolved on this update too).

→ **SSM-typed Parameters are always re-resolved on every `update-stack`/change-set operation, regardless of whether anything else changed.**

---

## T6 — Re-resolution of dynamic `{{resolve:ssm:...}}` references

Template resource:
```json
"TargetParam": {
  "Type": "AWS::SSM::Parameter",
  "Properties": { "Name": "/cfncompat/livetest/t6resource", "Type": "String",
                   "Value": "{{resolve:ssm:/cfncompat/livetest/str}}" }
}
```
Created while `str`=hello-v4 → `get-parameter` on `t6resource` = "hello-v4".

**T6a** Overwrite `str`→hello-v5, `update-stack` with the **identical** template: accepted (not a no-op error), `UPDATE_COMPLETE`. `get-parameter` on `t6resource` → **"hello-v5"**.

**T6b** Overwrite `str`→hello-v6, update template adding an unrelated `Handle2` resource (TargetParam text unchanged): `UPDATE_COMPLETE`. `get-parameter` → **"hello-v6"**.

**T6c** Overwrite `str`→hello-v7, update adding a `Description` to `TargetParam` itself: `UPDATE_COMPLETE`. `get-parameter` → **"hello-v7"**.

→ In all three cases the dynamic ssm reference re-resolved, confirming **`{{resolve:ssm:...}}` is unconditionally re-evaluated on every deploy operation that reaches that resource**, even when the template text for that resource is byte-identical to before.

**T6d** Changed Value to `{{resolve:ssm:/cfncompat/livetest/str:prod}}` (label): rejected synchronously:
```
aws: [ERROR]: An error occurred (ValidationError) when calling the UpdateStack operation: Incorrect format is used in the following SSM reference: [{{resolve:ssm:/cfncompat/livetest/str:prod}}]
```
→ **labels are not accepted in `{{resolve:ssm:...}}` either** (only numeric versions or unversioned).

**T6f** Changed Value to `{{resolve:ssm:/cfncompat/livetest/str:1}}`: `UPDATE_COMPLETE`, resolved to "hello-v1" — numeric version works fine.

**T6e** Changed Value to `{{resolve:ssm:/cfncompat/livetest/list}}` (StringList "a,b, c ,d"): `UPDATE_COMPLETE`, `get-parameter` → **"a,b, c ,d"** (untrimmed, raw) — contrast with T4a's trimmed "a,b,c,d" for the typed-Parameter `List<String>` path.

---

## T7 — Dynamic references in Parameters section and mid-string

**T7a** Parameter `Default: "{{resolve:ssm:/cfncompat/livetest/str}}"` (unversioned): rejected synchronously:
```
aws: [ERROR]: An error occurred (ValidationError) when calling the CreateStack operation: Template error: parameter P should not contain ssm versionless resolver
```

**T7b** Same but `Default: "{{resolve:ssm:/cfncompat/livetest/str:1}}"` (versioned): accepted, CREATE_COMPLETE, Output = "hello-v1".

→ **A dynamic reference used as a Parameter Default must pin a version.**

**T7c** Resource property `"Value": "prefix-{{resolve:ssm:/cfncompat/livetest/str:1}}-suffix"`: CREATE_COMPLETE, `get-parameter` → **"prefix-hello-v1-suffix"**.

**T7d** Two references in one string: `"A={{resolve:ssm:/cfncompat/livetest/str:1}}_B={{resolve:ssm:/cfncompat/livetest/strcommas:1}}"`: → **"A=hello-v1_B=x,y,z"**.

**T7e** Reference inside `Fn::Sub`: `{"Fn::Sub": "sub-prefix-{{resolve:ssm:/cfncompat/livetest/str:1}}-sub-suffix"}`: → **"sub-prefix-hello-v1-sub-suffix"**.

All mid-string / multi-ref / Fn::Sub-nested forms work exactly as documented.

---

## T8 — ssm-secure misuse

**T8a** `{{resolve:ssm-secure:/cfncompat/livetest/secure}}` (unversioned) as `AWS::SSM::Parameter/Value` (not an allowed property): `create-stack` call itself **succeeded** (StackId returned), but the stack then rolled back with a **stack-level** (not resource-level) event:
```
CREATE_IN_PROGRESS "SSM Secure reference is not supported in: [AWS::SSM::Parameter/Properties/Value]"
ROLLBACK_IN_PROGRESS "SSM Secure reference is not supported in: [AWS::SSM::Parameter/Properties/Value]. Rollback requested by user."
```

**T8b** Same but versioned `:1`: rejected **synchronously** at the CreateStack API call with the identical message:
```
aws: [ERROR]: An error occurred (ValidationError) when calling the CreateStack operation: SSM Secure reference is not supported in: [AWS::SSM::Parameter/Properties/Value]
```
→ **Same underlying rule, but sync vs async depending on whether a version is present in the reference.**

**T8c** `{{resolve:ssm-secure:/cfncompat/livetest/str:1}}` (ssm-secure against a plain String parameter): same synchronous rejection ("not supported in [...]") — the disallowed-property check fires before any type mismatch check would.

**T8d** `{{resolve:ssm:/cfncompat/livetest/secure:1}}` (plain `ssm`, not `ssm-secure`, against a SecureString): synchronous rejection:
```
aws: [ERROR]: An error occurred (ValidationError) when calling the CreateStack operation: Non-secure ssm prefix was used for secure parameter /cfncompat/livetest/secure
```

**T8e** `{{resolve:ssm-secure:/cfncompat/livetest/secure:prod}}` (label, after labelling v1 of `secure` as `prod`): synchronous rejection:
```
aws: [ERROR]: An error occurred (ValidationError) when calling the CreateStack operation: Incorrect format is used in the following SSM reference: [{{resolve:ssm-secure:/cfncompat/livetest/secure:prod}}]
```
Identical error shape to T6d — labels rejected the same way for `ssm-secure` as for `ssm`.

(Per instructions, no allowed-property test was performed — would require IAM/RDS resources.)

---

## T9 — Secrets Manager dynamic references

Multi-resource stack `t9`:
- `Password`: `{{resolve:secretsmanager:cfncompat-livetest/json:SecretString:password}}` → `get-parameter` = **"p2"** (AWSCURRENT)
- `PasswordPrev`: `...:password:AWSPREVIOUS` → **"p1"**
- `PlainWhole`: `{{resolve:secretsmanager:cfncompat-livetest/plain}}` (no suffix) → **"plaintext-secret"**
- `PlainSecretString`: `...:SecretString` → **"plaintext-secret"** (same)
- `JsonWhole`: `{{resolve:secretsmanager:cfncompat-livetest/json}}` (no key) → **`{"username":"u2","password":"p2"}`** (whole JSON string)
- `ArnUsername`: full ARN as secret-id, `:SecretString:username` → **"u2"**

Output `PasswordOut` (`Ref` to the `Password` resource) = the SSM parameter Name (as expected for `Ref` on `AWS::SSM::Parameter`), not the secret value.
Output `PasswordOutValue` (a **literal** `{{resolve:secretsmanager:...}}` string placed directly as an Output `Value`, not inside a resource property): came back as the **literal unresolved placeholder string** — `"{{resolve:secretsmanager:cfncompat-livetest/json:SecretString:password}}"`. **Dynamic references are not resolved when used directly as Output values.**

All resolved secret values land as **plain text** in the created (non-SecureString-typed) SSM parameters — confirmed via `get-parameter`. CloudFormation performs no automatic secrecy protection; that's on the template author (e.g., NoEcho only protects Parameters, not resource property values/Outputs).

**Error cases** (separate stacks, each failed at the **resource** level, not the stack level):
- `:SecretString:missingkey`: `ROLLBACK_COMPLETE`, resource `CREATE_FAILED` reason: **"Could not find a value associated with JSONKey in SecretString"**
- `cfncompat-livetest/plain:SecretString:password` (key lookup on a non-JSON secret): resource `CREATE_FAILED` reason: **"Could not parse SecretString JSON"**

**Re-resolution on unrelated update:** After `put-secret-value` (json secret → u3/p3, new AWSCURRENT), an `update-stack` that only added an unrelated `ExtraHandle` resource (Password's rendered properties text unchanged) → `UPDATE_COMPLETE`, but `get-parameter` on `t9-password` still returned **"p2"** — **not** re-resolved. Checked the stack events for `Password`: only the original `CREATE_*` events exist, no `UPDATE_*` event at all — CloudFormation determined that resource needed no update and skipped it entirely.

Then forcing an actual property change on `Password` itself (added a `Description`): `UPDATE_COMPLETE`, `get-parameter` → **"p3"** — re-resolved only once the resource was genuinely being updated for another reason.

→ **This is the sharpest contrast with `{{resolve:ssm:...}}` (T6b/T6c): ssm/ssm-secure dynamic references force re-resolution (and thus force that resource to be "updated") on every deploy touching the stack, but secretsmanager references only resolve when the resource is independently being updated.**

---

## T10 — Public (AWS-owned) SSM parameters

**T10a** `{{resolve:ssm:/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64}}`: `CREATE_COMPLETE`, `get-parameter` → "ami-081b0a6eac00b4f53" (matches the value fetched directly earlier).

**T10b** `AWS::SSM::Parameter::Value<AWS::EC2::Image::Id>` with that same public name as `ParameterValue`: `CREATE_COMPLETE`, `ResolvedValue` = "ami-081b0a6eac00b4f53".

Both work identically to a private/account-owned parameter — no special handling needed for the `/aws/service/...` namespace.

---

## Cleanup confirmation

All 36 test stacks deleted and verified `DELETE_COMPLETE` (list-stacks with prefix filter returns `[]` for any non-DELETE_COMPLETE status). All `/cfncompat/livetest/*` SSM parameters deleted (`get-parameters-by-path --recursive` returns `[]`). Both `cfncompat-livetest/*` secrets force-deleted without recovery (`list-secrets` filter returns `[]`). Resources created *by* test stacks (`t6resource`, `t7resource*`, `t8a`..`t8e` params, `t9-*` params, `t10-dynref`) were removed automatically as part of their owning stack's deletion.
