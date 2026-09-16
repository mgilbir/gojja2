# gojja2 - a pure Go, CPython-jinja2-compatible template engine.
#
# The reference test suites are NOT vendored. `make suites` clones them, at
# pinned revisions, into ./third_party/, which is gitignored, and `make import`
# turns them into conformance corpora under ./testdata/generated/, also
# gitignored.
#
# Five upstreams, each an independent reading of the language: Jinja's own
# pytest suite, MiniJinja's fixtures, minja, llama.cpp's Jinja tests, and two
# collections of real LLM chat templates. Only their *inputs* are used. Every
# expected output is regenerated from the pinned CPython jinja2, because that
# is the specification; where an upstream disagrees with it, it is wrong here.

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

.PHONY: suites
suites: $(THIRD_PARTY)/jinja/.stamp $(THIRD_PARTY)/minijinja/.stamp \
        $(THIRD_PARTY)/minja/.stamp $(THIRD_PARTY)/chat_templates/.stamp \
        $(THIRD_PARTY)/llamacpp/.stamp ## Download reference test suites (gitignored)

.PHONY: clean-suites
clean-suites: ## Remove downloaded suites
	rm -rf $(THIRD_PARTY)

# --- oracle ------------------------------------------------------------------

.PHONY: oracle
oracle: venv ## Regenerate golden files for testdata/corpus from CPython jinja2
	$(PY) tools/oracle/gen_corpus.py
	$(PY) tools/oracle/oracle.py --corpus testdata/corpus --golden testdata/golden

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

# --- tests -------------------------------------------------------------------

.PHONY: test
test: ## Run the Go test suite
	go test ./...

.PHONY: soak
soak: venv ## Differential-test N generated templates against CPython (make soak N=200000)
	GOJJA2_FUZZ_N=$(if $(N),$(N),50000) GOJJA2_FUZZ_SEED=$(if $(SEED),$(SEED),0) \
		go test ./conformance/ -run TestDifferential -timeout 60m -v

.PHONY: fuzz
fuzz: venv ## Coverage-guided differential fuzzing (make fuzz TIME=5m)
	go test ./conformance/ -run xxx -fuzz FuzzTemplate -fuzztime $(if $(TIME),$(TIME),1m)

.PHONY: conformance
conformance: ## Report conformance pass-rate against the full corpus
	go test ./conformance/... -run TestConformance -v

.PHONY: fmt
fmt: ## Format Go sources
	gofmt -l -w .

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: check
check: fmt-check vet test ## Everything CI should run

.PHONY: fmt-check
fmt-check: ## Fail if any source is unformatted
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "unformatted:"; echo "$$out"; exit 1; fi

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
