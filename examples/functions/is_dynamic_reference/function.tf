# is_dynamic_reference is the total counterpart of parse_dynamic_reference,
# which errors on anything that is not exactly one whole {{resolve:...}}
# reference. Test first, then parse.
output "is_reference" {
  value = provider::cfncompat::is_dynamic_reference("{{resolve:ssm:/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64}}")
  # => true
}

output "embedded_reference_is_not_one_whole_reference" {
  value = provider::cfncompat::is_dynamic_reference("ami is {{resolve:ssm:/a/b}}")
  # => false: splitting a string around its references and rebuilding it with
  #    interpolation is the synthesis backend's job.
}

output "plain_string" {
  value = provider::cfncompat::is_dynamic_reference("ami-0123456789abcdef0")
  # => false
}
