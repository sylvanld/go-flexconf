DOCS_PORT := 10001

.DEFAULT_GOAL := help

.PHONY: help docs-serve docs-build docs-clean

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

docs-serve: ## Serve the docs locally with live reload (http://127.0.0.1:$(DOCS_PORT))
	cd docs && uv run --isolated zensical serve -a 127.0.0.1:$(DOCS_PORT)

docs-build: ## Build the static docs site into docs/build
	cd docs && uv run --isolated zensical build --clean

docs-clean: ## Remove the built docs site
	rm -rf docs/build
