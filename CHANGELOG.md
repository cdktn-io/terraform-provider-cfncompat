# Changelog
All notable changes to this project will be documented in this file.

## Unreleased

FEATURES:

* **New Functions:** all pure-computable CloudFormation intrinsic functions as provider-defined functions: `base64`, `cidr`, `find_in_map`, `join`, `length`, `select`, `split`, `sub`, `to_json_string`, `condition_and`, `condition_contains`, `condition_each_member_equals`, `condition_each_member_in`, `condition_equals`, `condition_if`, `condition_not`, `condition_or` (see `RFCs/004-intrinsic-function-polyfill.md` for naming and exclusion rationale)
