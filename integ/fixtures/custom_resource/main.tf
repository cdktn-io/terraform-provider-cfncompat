terraform {
  required_providers {
    cfncompat = {
      source = "cdktn-io/cfncompat"
    }
    aws = {
      source = "hashicorp/aws"
    }
    archive = {
      source = "hashicorp/archive"
    }
  }
}

# Region comes from the AWS_REGION environment variable (see integ's
# terratest options, which default it to "us-east-1").
provider "aws" {}

# NOTE: provider "cfncompat" {} is intentionally left empty. A provider
# config cannot reference an attribute of a resource declared in the same
# config (aws_s3_bucket.response.bucket), so the response bucket is instead
# passed at the resource level via cfncompat_custom_resource.echo.response_bucket
# below.
provider "cfncompat" {}

variable "name_suffix" {
  type        = string
  description = "Unique suffix appended to every named resource, so parallel/repeat e2e runs never collide."
}

variable "greeting" {
  type        = string
  default     = "hello"
  description = "Value echoed back by the custom resource Lambda handler as data.Echo."
}

# --- Response transport bucket ------------------------------------------

resource "aws_s3_bucket" "response" {
  bucket        = "cfncompat-e2e-${var.name_suffix}"
  force_destroy = true
}

# --- Lambda execution role -----------------------------------------------

data "aws_iam_policy_document" "lambda_assume_role" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "lambda" {
  name               = "cfncompat-e2e-echo-${var.name_suffix}"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume_role.json
}

resource "aws_iam_role_policy_attachment" "lambda_logs" {
  role       = aws_iam_role.lambda.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}

# --- Echo handler ----------------------------------------------------------
#
# A dependency-free, standard CloudFormation custom resource handler: it
# always responds SUCCESS, echoes ResourceProperties.Greeting back as
# Data.Echo, and PUTs its response to event.ResponseURL with an empty
# Content-Type header (as CDK's own custom resource framework does).

data "archive_file" "echo" {
  type        = "zip"
  output_path = "${path.module}/.echo-lambda.zip"

  source {
    filename = "index.js"
    content  = <<-JS
      "use strict";
      const https = require("https");
      const { URL } = require("url");

      exports.handler = async (event, context) => {
        console.log("cfncompat e2e echo handler received:", JSON.stringify(event));

        const resourceProperties = event.ResourceProperties || {};
        const responseBody = JSON.stringify({
          Status: "SUCCESS",
          Reason: "See CloudWatch Logs: " + context.logGroupName,
          PhysicalResourceId:
            event.PhysicalResourceId || "cfncompat-e2e-physical-id",
          StackId: event.StackId,
          RequestId: event.RequestId,
          LogicalResourceId: event.LogicalResourceId,
          Data: {
            Echo: resourceProperties.Greeting,
          },
        });

        const responseUrl = new URL(event.ResponseURL);
        const options = {
          hostname: responseUrl.hostname,
          port: 443,
          path: responseUrl.pathname + responseUrl.search,
          method: "PUT",
          headers: {
            "content-type": "",
            "content-length": Buffer.byteLength(responseBody),
          },
        };

        await new Promise((resolve, reject) => {
          const request = https.request(options, (response) => {
            response.on("data", () => {});
            response.on("end", resolve);
          });
          request.on("error", reject);
          request.write(responseBody);
          request.end();
        });
      };
    JS
  }
}

resource "aws_lambda_function" "echo" {
  function_name    = "cfncompat-e2e-echo-${var.name_suffix}"
  role             = aws_iam_role.lambda.arn
  handler          = "index.handler"
  runtime          = "nodejs22.x"
  timeout          = 30
  filename         = data.archive_file.echo.output_path
  source_code_hash = data.archive_file.echo.output_base64sha256

  depends_on = [aws_iam_role_policy_attachment.lambda_logs]
}

# --- The custom resource under test ---------------------------------------

resource "cfncompat_custom_resource" "echo" {
  service_token   = aws_lambda_function.echo.arn
  resource_type   = "Custom::CfncompatE2E"
  service_timeout = 120

  resource_properties = {
    Greeting = var.greeting
  }

  response_bucket = aws_s3_bucket.response.bucket

  depends_on = [aws_iam_role_policy_attachment.lambda_logs]
}

output "physical_resource_id" {
  value = cfncompat_custom_resource.echo.physical_resource_id
}

output "echo" {
  value = cfncompat_custom_resource.echo.data.Echo
}
