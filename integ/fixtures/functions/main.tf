terraform {
  required_providers {
    cfncompat = {
      source = "cdktn-io/cfncompat"
    }
  }
}

provider "cfncompat" {}

# 1. base64 - Fn::Base64
output "base64" {
  value = provider::cfncompat::base64("AWS CloudFormation")
}

# 2. cidr - Fn::Cidr
output "cidr" {
  value = provider::cfncompat::cidr("192.168.0.0/24", 6, 5)
}

# 3. find_in_map - Fn::FindInMap
output "find_in_map" {
  value = provider::cfncompat::find_in_map({ a = { b = "v" } }, "a", "b")
}

output "find_in_map_default" {
  value = provider::cfncompat::find_in_map({ a = { b = "v" } }, "missing", "b", "fallback")
}

# 4. join - Fn::Join
output "join" {
  value = provider::cfncompat::join(":", ["a", "b", "c"])
}

# 9. length - Fn::Length
output "length" {
  value = provider::cfncompat::length(["a", "b", "c"])
}

# 5. select - Fn::Select
output "select" {
  value = provider::cfncompat::select(1, ["apples", "grapes", "oranges"])
}

# 6. split - Fn::Split
output "split" {
  value = provider::cfncompat::split(",", "a,b,c")
}

# 7. sub - Fn::Sub (literal "${" must be escaped as "$${" in HCL)
output "sub" {
  value = provider::cfncompat::sub("www.$${Domain}", { "Domain" = "example.com" })
}

# 8. to_json_string - Fn::ToJsonString
output "to_json_string" {
  value = provider::cfncompat::to_json_string({ k = "v" })
}

# 10-17. condition functions
output "condition_and" {
  value = provider::cfncompat::condition_and(true, true)
}

output "condition_or" {
  value = provider::cfncompat::condition_or(false, true)
}

output "condition_not" {
  value = provider::cfncompat::condition_not(false)
}

output "condition_equals" {
  value = provider::cfncompat::condition_equals("x", "x")
}

output "condition_if" {
  value = provider::cfncompat::condition_if(true, "a", "b")
}

output "condition_contains" {
  value = provider::cfncompat::condition_contains(["a", "b"], "a")
}

output "condition_each_member_equals" {
  value = provider::cfncompat::condition_each_member_equals(["a", "a"], "a")
}

output "condition_each_member_in" {
  value = provider::cfncompat::condition_each_member_in(["a"], ["a", "b"])
}
