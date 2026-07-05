# Community & Market Signals — companion to the PRD

**Purpose:** evidence that demand for *CDK-style authoring on a non-CloudFormation engine* is real, measurable, and recurring — and that the community keeps meeting it by **forking the high-level layer** (costly to maintain; most stall, one — CDK Terrain — actively carries it forward) rather than reusing AWS CDK's L2. That gap is exactly what the synthesis-backend seam closes. Companion to `000-PRD.md`; all figures are dated and sourced.

---

## 1. Usage data — Answers for AWS 2025 IaC survey

Among an **AWS-practitioner audience** ([answersforaws.com/2025/iac](https://answersforaws.com/2025/iac/), 2025; sample size not published on the results card):

| Tool | Usage |
|---|---:|
| AWS CloudFormation | **88.2%** |
| Terraform | **76.1%** |
| Cloud Development Kit (CDK) | **58.2%** |
| Ansible | 54.2% |
| OpenTofu | **15.2%** |
| Pulumi | **12.2%** |
| CDK for Terraform (CDKTF) | **11.9%** |
| Crossplane | 5.5% |
| Winglang | ~2% |

**Reads (note the AWS-audience skew — this is the PRD's exact population):**
- **CloudFormation (88%) and Terraform (76%) are *both* near-universal** → most AWS teams already straddle both engines. CDK authoring trapped on the CFN side is the daily friction.
- **CDK is at 58.2%** — a large installed base whose L2/L3 value is unreachable from the 76% who also run Terraform.
- **CDKTF (11.9%) ≈ Pulumi (12.2%)** — a dead heat for "CDK-style authoring beyond CloudFormation." Measurable, non-trivial demand.

*Caveat:* AWS-centric respondent pool (hence the high CFN/CDK numbers); the *relative* CDKTF≈Pulumi signal is the durable takeaway, not the absolute percentages.

---

## 2. The fork treadmill — and CDK Terrain, the active steward

Every attempt to give developers CDK-style ergonomics on Terraform/Pulumi **rebuilds the high-level layer from scratch** rather than reusing AWS CDK's L2 — a maintenance burden most efforts couldn't sustain, and that even the *active* fork carries largely alone.

### CDKTF (HashiCorp/IBM) — official upstream archived
- The CDK programming model emitting Terraform JSON; the `11.9%` above.
- **Archived December 10, 2025** — "no further features or fixes," repo read-only ([env0](https://www.env0.com/blog/another-one-bites-the-dust-what-the-cdktf-deprecation-means-for-you), [Pulumi](https://www.pulumi.com/blog/cdktf-is-deprecated-whats-next-for-your-team/)). HashiCorp/IBM walked away from the *official* CDK-on-Terraform.

### CDK Terrain (CDKTN) — the community fork that carries it forward
- When upstream was archived, CDK-on-Terraform did **not** die: [CDK Terrain (cdktn.io)](https://cdktn.io) forked CDKTF and keeps it alive — **steady releases** and **growing (if still modest) community contributions**. The ~12% cohort that wanted CDK-on-Terraform has a maintained path forward.
- CDK Terrain is the steward proposing *this* PRD. Its point: rather than carry a full parallel high-level layer forever, make the AWS CDK engine pluggable so the **real AWS CDK L2** renders to Terraform/OpenTofu — sustainable for the whole ecosystem, not just one fork's maintainers.

### SST v3 ("Ion") — rewrote *off* AWS CDK/CloudFormation
- SST spent ~3 years on **AWS CDK + CloudFormation**, hit its limits, and in v3 **replaced the entire engine with Pulumi (runtime) + Terraform providers** — their post is titled *["Moving away from CDK"](https://sst.dev/blog/moving-away-from-cdk/)* ([SST v3 announcement](https://sst.dev/blog/sst-v3/); [Pulumi: "why SST switched"](https://www.pulumi.com/blog/aws-cdk-vs-pulumi-why-sst-switched/)).
- Stated reasons map 1:1 to this PRD's gaps: CloudFormation's **500-resource stack limit**, CDK/CFN design constraints, and the desire for **multi-cloud** (Cloudflare, Vercel, Stripe, GitHub) — "the limitations of CDK and the underlying CloudFormation were holding them back."
- **But SST rebuilt its own component layer on Pulumi** — i.e. another *fork*, not a reuse of AWS CDK's L2. And momentum has since shifted: the **same founders launched OpenCode (Jun 19 2025)**, now in hypergrowth, while SST core development slowed.
  - *Velocity contrast (GitHub API, as of 2026-06-23):* `sst/opencode` — **177k★**, latest release **v1.17.9 (Jun 21 2026)**, multiple commits **Jun 22 2026**. `sst/sst` — **26.1k★** (now v4), latest release **v4.15.2 (May 29 2026)**, last commit **Jun 4 2026**. SST is maintained but clearly no longer the team's primary focus.
- Background: [Dax Raad](https://www.baseten.co/blog/building-ai-agents-open-code-and-open-source-a-conversation-with-dax/) created both SST and OpenCode; OpenCode reportedly reached ~8M MAU by mid-2026.

### TerraConstructs — CDK L2 fork onto `provider-aws`
- An AWS-CDK-L2 port targeting the Terraform `aws` provider (prior art analyzed in `02-independent-research/`). Proves the model works, but the `provider-aws` schema **diverges from CloudFormation** (one S3 bucket → nine TF resources), forcing a **fully separate L2 codebase** whose coverage permanently lags. Unsustainable for the same reason.

### Pulumi — viable, but a different model
- The credible alternative, but it's **its own SDK and programming model**, not AWS CDK's L2 — adoption stays ~12% and suits teams who unify app + platform languages, per [Matt Gowie / Masterpoint](https://newsletter.masterpoint.io/) commentary.

---

## 3. The synthesis

Multiple independent efforts each concluded that **developers want CDK-style ergonomics on a non-CloudFormation engine** — and each **rebuilt the high-level layer from scratch**:

- CDKTF — official upstream **archived** (Dec 2025).
- **CDK Terrain (CDKTN)** — the **active community fork** that continues it: steady releases, growing contributions. Proof the demand is real enough to sustain a fork — and that the fork still has to carry a parallel high-level layer largely alone.
- SST v3 — its **own Pulumi component layer**; team focus moved to OpenCode.
- TerraConstructs — a **`provider-aws` L2 fork** that can't keep pace.

The demand is repeatedly proven, and CDK Terrain shows it's sustainable to keep CDK-on-Terraform shipping — but *every* fork still re-implements and maintains its own high-level layer. **No one has made the AWS CDK engine pluggable so the *single, real AWS CDK L2 library* renders to Terraform/OpenTofu (or Pulumi).** That is the synthesis-backend seam in `000-PRD.md`: keep one L2 codebase, swap the output backend — so CDK Terrain (and any other backend) implements *just the engine*, not a whole parallel L2. The fork treadmill becomes a shared foundation.

---

## Sources
- Answers for AWS 2025 IaC survey — https://answersforaws.com/2025/iac/ (results card: https://answersforaws.com/social/2025/results-iac.gif)
- SST "Moving away from CDK" — https://sst.dev/blog/moving-away-from-cdk/ · SST v3 — https://sst.dev/blog/sst-v3/ · Pulumi "why SST switched" — https://www.pulumi.com/blog/aws-cdk-vs-pulumi-why-sst-switched/
- CDKTF deprecation (Dec 10 2025) — https://www.env0.com/blog/another-one-bites-the-dust-what-the-cdktf-deprecation-means-for-you · https://www.pulumi.com/blog/cdktf-is-deprecated-whats-next-for-your-team/
- OpenCode / Dax Raad — https://www.baseten.co/blog/building-ai-agents-open-code-and-open-source-a-conversation-with-dax/
- GitHub activity (sst/sst, sst/opencode) — GitHub REST API, retrieved 2026-06-23
- IaC Insights (Matt Gowie / Masterpoint) — https://newsletter.masterpoint.io/
