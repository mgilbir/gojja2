# gojja2 - a pure Go, CPython-jinja2-compatible template engine.
#
# The reference test suites (Jinja's pytest suite, MiniJinja's fixture corpus)
# are NOT vendored. `make suites` clones them, at pinned revisions, into
# ./third_party/, which is gitignored.

SHELL := /bin/bash
.DEFAULT_GOAL := help

# --- pinned references -------------------------------------------------------
# The Jinja checkout and the oracle interpreter MUST stay on the same version:
# expected output is whatever this exact CPython jinja2 produces.
JINJA_VERSION    := 3.1.6
JINJA_REPO       := https://github.com/pallets/jinja.git
JINJA_REV        := 2d4ce43010630478ee88b463f731389fa18953f4   # refs/tags/3.1.6

MINIJINJA_REPO   := https://github.com/mitsuhiko/minijinja.git
MINIJINJA_REV    := 3c4034f62a18db2b8d3ee708fd32a98a625b93a0   # minijinja-go/v3.0.0-alpha.1

THIRD_PARTY := third_party
VENV        := .venv
PY          := $(VENV)/bin/python

.PHONY: help
help: ## Show this help
	@awk 'BEGIN{FS=":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# --- toolchain ---------------------------------------------------------------

$(VENV)/.stamp:
	uv venv $(VENV)
	uv pip install --python $(PY) "jinja2==$(JINJA_VERSION)"
	@touch $@

.PHONY: venv
venv: $(VENV)/.stamp ## Create the oracle virtualenv (CPython + jinja2)

$(THIRD_PARTY)/jinja/.stamp:
	@mkdir -p $(THIRD_PARTY)
	rm -rf $(THIRD_PARTY)/jinja
	git clone --quiet $(JINJA_REPO) $(THIRD_PARTY)/jinja
	git -C $(THIRD_PARTY)/jinja checkout --quiet $(JINJA_REV)
	@touch $@

$(THIRD_PARTY)/minijinja/.stamp:
	@mkdir -p $(THIRD_PARTY)
	rm -rf $(THIRD_PARTY)/minijinja
	git clone --quiet $(MINIJINJA_REPO) $(THIRD_PARTY)/minijinja
	git -C $(THIRD_PARTY)/minijinja checkout --quiet $(MINIJINJA_REV)
	@touch $@

.PHONY: suites
suites: $(THIRD_PARTY)/jinja/.stamp $(THIRD_PARTY)/minijinja/.stamp ## Download reference test suites (gitignored)

.PHONY: clean-suites
clean-suites: ## Remove downloaded suites
	rm -rf $(THIRD_PARTY)

# --- oracle ------------------------------------------------------------------

.PHONY: oracle
oracle: venv ## Regenerate golden files for testdata/corpus from CPython jinja2
	$(PY) tools/oracle/oracle.py --corpus testdata/corpus --golden testdata/golden

.PHONY: oracle-check
oracle-check: venv ## Verify committed goldens still match CPython jinja2
	$(PY) tools/oracle/oracle.py --corpus testdata/corpus --golden testdata/golden --check

# --- imported corpora --------------------------------------------------------

.PHONY: import
import: suites venv ## Import MiniJinja's fixtures and record jinja2's answers
	$(PY) tools/oracle/import_minijinja.py
	$(PY) tools/oracle/oracle.py \
		--corpus testdata/generated/minijinja \
		--golden testdata/generated/minijinja-golden
	$(PY) tools/oracle/harvest_jinja.py
	$(PY) tools/oracle/oracle.py \
		--corpus testdata/generated/jinja-harvest \
		--golden testdata/generated/jinja-harvest-golden

# --- tests -------------------------------------------------------------------

.PHONY: test
test: ## Run the Go test suite
	go test ./...

.PHONY: conformance
conformance: ## Report conformance pass-rate against the full corpus
	go test ./conformance/... -run TestConformance -v

.PHONY: fmt
fmt: ## Format Go sources
	gofmt -l -w .

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: clean
clean: ## Remove build and generated artifacts
	rm -rf bin testdata/generated

.PHONY: repr-corpus
repr-corpus: venv ## Regenerate the CPython repr() corpus used by value tests
	$(PY) tools/oracle/gen_repr.py

.PHONY: ops-corpus
ops-corpus: venv ## Regenerate the CPython operator corpus used by value tests
	$(PY) tools/oracle/gen_ops.py

.PHONY: lex-corpus
lex-corpus: venv ## Regenerate the jinja2 token-stream corpus used by lexer tests
	$(PY) tools/oracle/gen_lex.py

.PHONY: parse-corpus
parse-corpus: venv ## Regenerate the jinja2 AST corpus used by parser tests
	$(PY) tools/oracle/gen_parse.py

.PHONY: corpora
corpora: repr-corpus ops-corpus lex-corpus parse-corpus oracle ## Regenerate every oracle-derived corpus

.PHONY: ask
ask: venv ## Ask the oracle what CPython jinja2 renders: make ask T='{{ 1/2 }}'
	@$(PY) tools/oracle/oracle.py --template '$(T)' --context '$(if $(C),$(C),{})'
