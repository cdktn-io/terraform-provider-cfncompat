include ./common.mk

functions: ## Run TestE2EFunctions (no AWS credentials needed)
	go test -v -count 1 -timeout 15m -run '^TestE2EFunctions$$' ./...
.PHONY: functions

custom-resource: ## Run TestE2ECustomResource (needs real AWS credentials)
	CFNCOMPAT_E2E_AWS=1 go test -v -count 1 -timeout 30m -run '^TestE2ECustomResource$$' ./...
.PHONY: custom-resource

all: functions custom-resource ## Run every e2e test (functions, then custom-resource)
.PHONY: all
