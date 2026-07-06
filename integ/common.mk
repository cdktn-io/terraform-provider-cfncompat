help: ## Print out every target with a description
	@awk 'BEGIN { FS = ":.*##" } \
	/^[a-zA-Z0-9_.-]+:.*##/ { \
		target = $$1; desc = $$2; \
		if (desc ~ /^ Test/) { \
			test_targets[target] = desc; \
			test_names[++test_count] = target; \
		} else { \
			other_targets[target] = desc; \
			other_names[++other_count] = target; \
		} \
	} \
	END { \
		if (test_count > 0) { \
			print "\nTest Targets:"; \
			for (i = 1; i <= test_count; i++) { \
				t = test_names[i]; \
				printf "  \033[36m%-25s\033[0m %s\n", t, test_targets[t]; \
			} \
		} \
		if (other_count > 0) { \
			print "\nOther Targets:"; \
			for (i = 1; i <= other_count; i++) { \
				t = other_names[i]; \
				printf "  \033[36m%-25s\033[0m %s\n", t, other_targets[t]; \
			} \
		} \
	}' $(MAKEFILE_LIST)
	@echo ""
	@echo "Special pattern targets:"
	@awk '/^## %/ { sub(/^## /, "  "); print }' $(MAKEFILE_LIST)
.PHONY: help

## %-no-cleanup:              Skip the cleanup (destroy) stage (i.e. functions-no-cleanup)
%-no-cleanup:
	SKIP_cleanup=true $(MAKE) $*
.PHONY: %-no-cleanup

## %-cleanup-only:            Only run the cleanup (destroy) stage, against a prior run's state (i.e. functions-cleanup-only)
%-cleanup-only:
	SKIP_build_provider=true SKIP_deploy=true SKIP_validate=true $(MAKE) $*
.PHONY: %-cleanup-only

## %-validate-only:           Skip build/deploy and cleanup, re-run just the validate stage against a prior run's state (i.e. functions-validate-only)
%-validate-only:
	SKIP_build_provider=true SKIP_deploy=true SKIP_cleanup=true $(MAKE) $*
.PHONY: %-validate-only

clean: ## Remove all local e2e working state (tf/, .build/)
	rm -rf tf/
	rm -rf .build/
.PHONY: clean
