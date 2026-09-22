# gojja2 - a pure Go, CPython-jinja2-compatible template engine.
#
# The reference test suites are NOT vendored. `make suites` clones them, at
# pinned revisions, into ./third_party/, which is gitignored, and `make import`
# turns them into conformance corpora under ./testdata/generated/, also
# gitignored.
#
# Ten upstreams: Jinja's own pytest suite, MiniJinja's fixtures, minja,
# llama.cpp's Jinja tests, two collections of real LLM chat templates, a
# documentation theme, and four cookiecutter project templates. Only their
# *inputs* are used. Every expected output is regenerated from the pinned
# CPython jinja2, because that is the specification; where an upstream
# disagrees with it, it is wrong here. NOTICE names each one with its licence.

SHELL := /bin/bash
.DEFAULT_GOAL := help

# --- pinned references -------------------------------------------------------
# The Jinja checkout and the oracle interpreter MUST stay on the same version:
# expected output is whatever this exact CPython jinja2 produces.
#
# PYTHON_VERSION is part of the specification and not a convenience. CPython
# carries its own Unicode, so the interpreter decides which code points are
# digits, how repr escapes them, and what every case-mapping table here says --
# 3.11 is Unicode 14.0.0, 3.13 is 15.1.0 and 3.14 is 16.0.0. This used to be
# whatever `uv venv` found on the machine, which meant the specification was
# chosen by accident and a contributor on a different interpreter would
# regenerate different goldens.
#
# It must stay in step with value.DefaultPythonVersion, which is the same fact
# for a caller who does not choose; TestDefaultVersionMatchesThePin fails if a
# bump moves one and not the other. Every other version gojja2 reproduces is
# recorded as a set of differences from this one -- see `make golden-matrix`,
# `make arity-matrix` and `make unicode-matrix` -- so moving it rotates which
# version needs no overrides and which ones do.
PYTHON_VERSION   := 3.13
JINJA_VERSION    := 3.1.6
# markupsafe travels in every golden and decides what escaping does, so it is
# pinned for the same reason the interpreter is: it was resolved freely before,
# which meant a fresh venv could quietly regenerate against a different one.
MARKUPSAFE_VERSION := 3.0.3
JINJA_REPO       := https://github.com/pallets/jinja.git
JINJA_REV        := 2d4ce43010630478ee88b463f731389fa18953f4   # refs/tags/3.1.6

MINIJINJA_REPO   := https://github.com/mitsuhiko/minijinja.git
MINIJINJA_REV    := 3c4034f62a18db2b8d3ee708fd32a98a625b93a0   # minijinja-go/v3.0.0-alpha.1

# Three more independent readings of the language, for breadth the corpora
# above do not reach. Inputs only: every expected output is regenerated from
# CPython jinja2, whatever these projects believe.
MINJA_REPO       := https://github.com/google/minja.git
MINJA_REV        := 021c2293c187789ef13d56c6cfd89c9b134fd80f

CHAT_TEMPLATES_REPO := https://github.com/chujiezheng/chat_templates.git
CHAT_TEMPLATES_REV  := 11c495621569969f264ee70b5c8bb49ba7a1a410

# llama.cpp is cloned sparsely: the whole repository is gigabytes and all that
# is wanted is its Jinja tests and its vendored model templates.
LLAMACPP_REPO    := https://github.com/ggml-org/llama.cpp.git
LLAMACPP_REV     := fb27a525d28381a16a4bb038858a10e4927381ca
LLAMACPP_PATHS   := models/templates tests

# Templates written to be used rather than tested: inheritance, partials and
# blocks, which the chat-template corpora have none of. Also cloned sparsely.
MKDOCS_REPO      := https://github.com/squidfunk/mkdocs-material.git
MKDOCS_REV       := 1c73dca3ff4909e4cddd0d3b6e272298e902dec7
MKDOCS_PATHS     := material/templates

# Cookiecutter project templates. These are the only imported corpus that
# arrives with its own context -- cookiecutter.json is one, in JSON, written by
# the template's author. Name, repository and pinned revision, one per line.
COOKIECUTTERS := \
  cookiecutter_django:cookiecutter/cookiecutter-django:b17f6c03da7d125e414c737cc31233c388bcebb6 \
  cookiecutter_pypackage:audreyfeldroy/cookiecutter-pypackage:ced42cf27e20987ad5e1a315a26e89a94002ba74 \
  cookiecutter_datascience:drivendata/cookiecutter-data-science:0f6b163cdbe3918a2c65ab57ad9fefda93976d9e \
  cookiecutter_hypermodern:cjolowicz/cookiecutter-hypermodern-python:af0fd99e72e3afac2dc2b20406b1bee689260be1

THIRD_PARTY := third_party
VENV        := .venv
PY          := $(VENV)/bin/python

.PHONY: help
help: ## Show this help
	@awk 'BEGIN{FS=":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# --- toolchain ---------------------------------------------------------------

$(VENV)/.stamp:
	uv venv --python $(PYTHON_VERSION) $(VENV)
	uv pip install --python $(PY) "jinja2==$(JINJA_VERSION)" "markupsafe==$(MARKUPSAFE_VERSION)"
	@$(PY) -c 'import sys, unicodedata; \
	  v = ".".join(map(str, sys.version_info[:2])); \
	  assert v == "$(PYTHON_VERSION)", f"venv is {v}, not $(PYTHON_VERSION)"; \
	  import importlib.metadata as md; \
	  ms = md.version("markupsafe"); \
	  assert ms == "$(MARKUPSAFE_VERSION)", f"markupsafe is {ms}, not $(MARKUPSAFE_VERSION)"; \
	  print(f"oracle: CPython {sys.version.split()[0]}, Unicode {unicodedata.unidata_version}")'
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

$(THIRD_PARTY)/minja/.stamp:
	@mkdir -p $(THIRD_PARTY)
	rm -rf $(THIRD_PARTY)/minja
	git clone --quiet $(MINJA_REPO) $(THIRD_PARTY)/minja
	git -C $(THIRD_PARTY)/minja checkout --quiet $(MINJA_REV)
	@touch $@

$(THIRD_PARTY)/chat_templates/.stamp:
	@mkdir -p $(THIRD_PARTY)
	rm -rf $(THIRD_PARTY)/chat_templates
	git clone --quiet $(CHAT_TEMPLATES_REPO) $(THIRD_PARTY)/chat_templates
	git -C $(THIRD_PARTY)/chat_templates checkout --quiet $(CHAT_TEMPLATES_REV)
	@touch $@

# Fetched by SHA rather than cloned, so the checkout is shallow, blobless and
# pinned all at once -- `git clone --depth 1` can only take a branch tip.
$(THIRD_PARTY)/llamacpp/.stamp:
	@mkdir -p $(THIRD_PARTY)
	rm -rf $(THIRD_PARTY)/llamacpp
	git init --quiet $(THIRD_PARTY)/llamacpp
	git -C $(THIRD_PARTY)/llamacpp remote add origin $(LLAMACPP_REPO)
	git -C $(THIRD_PARTY)/llamacpp sparse-checkout init --cone
	git -C $(THIRD_PARTY)/llamacpp sparse-checkout set $(LLAMACPP_PATHS)
	git -C $(THIRD_PARTY)/llamacpp fetch --quiet --depth 1 --filter=blob:none \
		origin $(LLAMACPP_REV)
	git -C $(THIRD_PARTY)/llamacpp checkout --quiet FETCH_HEAD
	@touch $@

$(THIRD_PARTY)/mkdocs_material/.stamp:
	@mkdir -p $(THIRD_PARTY)
	rm -rf $(THIRD_PARTY)/mkdocs_material
	git init --quiet $(THIRD_PARTY)/mkdocs_material
	git -C $(THIRD_PARTY)/mkdocs_material remote add origin $(MKDOCS_REPO)
	git -C $(THIRD_PARTY)/mkdocs_material sparse-checkout init --cone
	git -C $(THIRD_PARTY)/mkdocs_material sparse-checkout set $(MKDOCS_PATHS)
	git -C $(THIRD_PARTY)/mkdocs_material fetch --quiet --depth 1 --filter=blob:none \
		origin $(MKDOCS_REV)
	git -C $(THIRD_PARTY)/mkdocs_material checkout --quiet FETCH_HEAD
	@touch $@

# One clone rule per cookiecutter template. They are all the same shape, so
# the rule is written once and instantiated rather than copied four times.
define cookiecutter_rule
$(THIRD_PARTY)/$(1)/.stamp:
	@mkdir -p $(THIRD_PARTY)
	rm -rf $(THIRD_PARTY)/$(1)
	git init --quiet $(THIRD_PARTY)/$(1)
	git -C $(THIRD_PARTY)/$(1) remote add origin https://github.com/$(2).git
	git -C $(THIRD_PARTY)/$(1) fetch --quiet --depth 1 origin $(3)
	git -C $(THIRD_PARTY)/$(1) checkout --quiet FETCH_HEAD
	@touch $$@
endef

# Split on the colons, then instantiate. The call is kept on one line: a
# continuation inside it would carry the indentation into the arguments, and a
# target named " third_party/..." has no rule anyone can match.
cc_name = $(word 1,$(subst :, ,$(1)))
cc_repo = $(word 2,$(subst :, ,$(1)))
cc_rev  = $(word 3,$(subst :, ,$(1)))

$(foreach c,$(COOKIECUTTERS),$(eval $(call cookiecutter_rule,$(call cc_name,$(c)),$(call cc_repo,$(c)),$(call cc_rev,$(c)))))

COOKIECUTTER_STAMPS := $(foreach c,$(COOKIECUTTERS),$(THIRD_PARTY)/$(call cc_name,$(c))/.stamp)

.PHONY: suites
suites: $(COOKIECUTTER_STAMPS) $(THIRD_PARTY)/jinja/.stamp $(THIRD_PARTY)/minijinja/.stamp \
        $(THIRD_PARTY)/minja/.stamp $(THIRD_PARTY)/chat_templates/.stamp \
        $(THIRD_PARTY)/llamacpp/.stamp $(THIRD_PARTY)/mkdocs_material/.stamp \
        ## Download reference test suites (gitignored)

.PHONY: clean-suites
clean-suites: ## Remove downloaded suites
	rm -rf $(THIRD_PARTY)

# --- oracle ------------------------------------------------------------------

.PHONY: oracle
oracle: venv arity methodarity arity-matrix entities decimal strclass casemap utf8 unicode-matrix ## Regenerate golden files for testdata/corpus from CPython jinja2
	$(PY) tools/oracle/gen_corpus.py
	$(PY) tools/oracle/oracle.py --corpus testdata/corpus --golden testdata/golden
	$(PY) tools/oracle/gen_golden_matrix.py
	$(PY) tools/oracle/gen_syntax.py
	$(PY) tools/oracle/gen_nameflow.py

.PHONY: arity
arity: venv ## Regenerate arity.go from jinja2's own filter and test signatures
	$(PY) tools/oracle/gen_arity.py
	gofmt -w arity.go

.PHONY: methodarity
methodarity: venv ## Regenerate method_arity.go from CPython's own built-in methods
	$(PY) tools/oracle/gen_methods.py
	gofmt -w method_arity.go

.PHONY: entities
entities: venv ## Regenerate entities.go from CPython's HTML character references
	$(PY) tools/oracle/gen_entities.py
	gofmt -w entities.go

.PHONY: strclass
strclass: venv ## Regenerate strclass.go from CPython's numeric character classes
	$(PY) tools/oracle/gen_strclass.py
	gofmt -w strclass.go

.PHONY: casemap
casemap: venv ## Regenerate casemap.go from CPython's full case mappings
	$(PY) tools/oracle/gen_casemap.py
	gofmt -w casemap.go

.PHONY: arity-matrix
arity-matrix: venv ## Regenerate method_arity_other.go from every CPython gojja2 reproduces
	$(PY) tools/oracle/gen_arity_matrix.py
	gofmt -w method_arity_other.go

.PHONY: golden-matrix
golden-matrix: venv ## Regenerate testdata/golden-<version> from every CPython gojja2 reproduces
	$(PY) tools/oracle/gen_golden_matrix.py

.PHONY: nameflow
nameflow: venv ## Regenerate testdata/nameflow: what each case does with the caller's variables
	$(PY) tools/oracle/gen_nameflow.py

.PHONY: syntax
syntax: venv ## Regenerate testdata/syntax.jsonl: jinja2's parse tree in gojja2's vocabulary
	$(PY) tools/oracle/gen_syntax.py

.PHONY: unicode-matrix
unicode-matrix: venv ## Regenerate value/unicode_other.go from every CPython gojja2 reproduces
	$(PY) tools/oracle/gen_unicode_matrix.py
	gofmt -w value/unicode_other.go

.PHONY: decimal
decimal: venv ## Regenerate value/decimaltable.go from CPython's decimal digits
	$(PY) tools/oracle/gen_decimal.py
	gofmt -w value/decimaltable.go

.PHONY: utf8
utf8: venv ## Regenerate utf8digest.go from CPython's own UTF-8 decoder
	$(PY) tools/oracle/gen_utf8.py
	gofmt -w utf8digest.go

.PHONY: oracle-check
oracle-check: venv ## Verify committed goldens still match CPython jinja2
	$(PY) tools/oracle/oracle.py --corpus testdata/corpus --golden testdata/golden --check

# --- imported corpora --------------------------------------------------------

.PHONY: import
import: suites venv ## Build every imported corpus and record jinja2's answers
	$(PY) tools/oracle/import_minijinja.py
	$(PY) tools/oracle/oracle.py \
		--corpus testdata/generated/minijinja \
		--golden testdata/generated/minijinja-golden
	$(PY) tools/oracle/harvest_jinja.py
	$(PY) tools/oracle/oracle.py \
		--corpus testdata/generated/jinja-harvest \
		--golden testdata/generated/jinja-harvest-golden
	$(PY) tools/oracle/import_minja.py
	$(PY) tools/oracle/oracle.py \
		--corpus testdata/generated/minja \
		--golden testdata/generated/minja-golden
	$(PY) tools/oracle/import_llamacpp.py
	$(PY) tools/oracle/oracle.py \
		--corpus testdata/generated/llamacpp \
		--golden testdata/generated/llamacpp-golden
	$(PY) tools/oracle/import_chat_templates.py
	$(PY) tools/oracle/oracle.py \
		--corpus testdata/generated/chat-templates \
		--golden testdata/generated/chat-templates-golden
	$(PY) tools/oracle/import_wild.py
	$(PY) tools/oracle/oracle.py \
		--corpus testdata/generated/wild \
		--golden testdata/generated/wild-golden
	$(PY) tools/oracle/import_cookiecutter.py
	$(PY) tools/oracle/oracle.py \
		--corpus testdata/generated/cookiecutter \
		--golden testdata/generated/cookiecutter-golden
	$(PY) tools/oracle/report_minijinja.py
	$(PY) tools/oracle/gen_generated_refs.py

.PHONY: divergence-report
divergence-report: venv ## Report where MiniJinja disagrees with CPython jinja2
	$(PY) tools/oracle/report_minijinja.py

# --- tests -------------------------------------------------------------------

.PHONY: test
test: ## Run the Go test suite
	go test ./...

.PHONY: mutate
mutate: venv ## Break the analysis on purpose and report what nothing notices
	python3 tools/mutate.py

.PHONY: soak-syntax
soak-syntax: venv ## Differential-test the structure and analyses: make soak-syntax N=200000 SEED=7
	GOJJA2_FUZZ_N=$(if $(N),$(N),50000) GOJJA2_FUZZ_SEED=$(if $(SEED),$(SEED),0) \
		go test ./conformance/ -run 'TestSyntaxDifferential|TestEncodingTheSameMeans' \
		-timeout 60m -v

.PHONY: soak
soak: venv ## Differential-test generated templates: make soak N=200000 SEED=7
	GOJJA2_FUZZ_N=$(if $(N),$(N),50000) GOJJA2_FUZZ_SEED=$(if $(SEED),$(SEED),0) \
		go test ./conformance/ -run TestDifferential -timeout 60m -v

.PHONY: fuzz
fuzz: venv ## Coverage-guided differential fuzzing (make fuzz TIME=5m)
	go test ./conformance/ -run xxx -fuzz '^FuzzTemplate$$' -fuzztime $(if $(TIME),$(TIME),1m)

# No venv, deliberately. `fuzz` above is the sharper tool and it cannot run
# where there is no CPython with jinja2 installed -- which is everywhere CI
# runs, so the fuzzer that could have run on every commit was the one nobody
# ran. This one asserts what the engine owes every input regardless of meaning:
# no panic, no overrun, no writing past the bound, no unclassifiable error.
.PHONY: fuzz-props
fuzz-props: ## Coverage-guided property fuzzing, no oracle (make fuzz-props TIME=5m)
	go test . -run xxx -fuzz '^FuzzParse$$' -fuzztime $(if $(TIME),$(TIME),1m)
	go test . -run xxx -fuzz '^FuzzRender$$' -fuzztime $(if $(TIME),$(TIME),1m)
	go test . -run xxx -fuzz '^FuzzAutoescape$$' -fuzztime $(if $(TIME),$(TIME),1m)
	go test . -run xxx -fuzz '^FuzzRenderData$$' -fuzztime $(if $(TIME),$(TIME),1m)

.PHONY: conformance
conformance: ## Report conformance pass-rate against the full corpus
	go test ./conformance/... -run TestConformance -v

# third_party/ holds pinned upstream checkouts, one of which (minijinja-go) is
# a Go module of ~40 files. `gofmt -w .` would rewrite someone else's tree in
# place and leave the clone dirty against its pin, so both targets below filter
# it out -- which is also exactly what the CI workflow does. The two must agree:
# a `make check` that passes where CI fails is worse than no check at all.
.PHONY: fmt
fmt: ## Format this repository's Go sources (third_party/ is left alone)
	@out=$$(gofmt -l . | grep -v '^third_party/' || true); \
	if [ -z "$$out" ]; then echo "already formatted"; else \
		printf '%s\n' "$$out" | while IFS= read -r f; do gofmt -w "$$f"; done; \
		echo "formatted:"; printf '%s\n' "$$out"; fi

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: lint
lint: ## Run golangci-lint, pinned to the version CI uses
	golangci-lint run

# Everything CI runs, in CI's order. `memlimit` is not decoration: it is a step
# the workflow has and this target did not, so a contributor could run `make
# check`, see it pass, push, and fail CI on a step they had no way to reach.
.PHONY: check
check: fmt-check vet test memlimit race lint ## Everything CI runs

# A second pass under a tight GOMEMLIMIT, which changes when the collector runs
# and so exercises the allocation paths differently.
#
# -count=1 is load-bearing. GOMEMLIMIT is read by the runtime rather than
# through os.Getenv, so it is not part of the test cache key: without it this
# replays the `test` target above, reports every package as "(cached)" and
# executes no test code at all. That is what the CI step used to do.
#
# It is also not a cap in the sense the name suggests. GOMEMLIMIT is a *soft*
# limit -- the runtime collects harder rather than refusing an allocation -- so
# a regression shows up here as a slow run, not a failure. What fails on one is
# the guards that measure the property directly: TestSlicingCostsTheResultNotThe
# Input, TestTupleHashIsLinear, TestLexingIsLinearInTheSource and the deep-graph
# tests.
.PHONY: memlimit
memlimit: ## Run the suite again under a tight GOMEMLIMIT, as CI does
	GOMEMLIMIT=1GiB go test -count=1 ./...

.PHONY: race
race: ## Run the Go test suite under the race detector
	go test -race ./...

.PHONY: fmt-check
fmt-check: ## Fail if any source this repository owns is unformatted
	@out=$$(gofmt -l . | grep -v '^third_party/' || true); \
	if [ -n "$$out" ]; then echo "unformatted:"; printf '%s\n' "$$out"; exit 1; fi

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
ask: venv ## Ask the oracle what jinja2 renders: make ask T='{{ a }}' C='{"a":1}'
	@$(PY) tools/oracle/oracle.py --template '$(T)' --context '$(if $(C),$(C),{})'
