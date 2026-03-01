CONFIG = config.json

ifneq ($(wildcard $(CONFIG)),)
SOURCE_IMAGE  := $(shell python3 -c "import json;c=json.load(open('$(CONFIG)'));s=c['source'];print(s['registry']+'/'+s['image']+':'+s['tag'])")
BRIDGE_REG    := $(shell python3 -c "import json;print(json.load(open('$(CONFIG)'))['bridge']['registry'])")
BRIDGE_IMAGE  := $(shell python3 -c "import json;print(json.load(open('$(CONFIG)'))['bridge']['image'])")
SIDECAR_REG   := $(shell python3 -c "import json;print(json.load(open('$(CONFIG)'))['sidecar']['registry'])")
SIDECAR_IMAGE := $(shell python3 -c "import json;print(json.load(open('$(CONFIG)'))['sidecar']['image'])")
GIT_TAG       := $(shell python3 configure.py --compute-tag)
endif

BRIDGE_REF  = $(BRIDGE_REG)/$(BRIDGE_IMAGE):$(GIT_TAG)
SIDECAR_REF = $(SIDECAR_REG)/$(SIDECAR_IMAGE):$(GIT_TAG)

.PHONY: configure build push clean sidecar-docs sidecar-build sidecar-push \
       compose-up compose-down compose-logs compose-ps

configure:
	python3 configure.py

build: $(CONFIG)
	docker build --platform=linux/amd64 --build-arg BASE_IMAGE=$(SOURCE_IMAGE) -t $(BRIDGE_REF) build

push: build
	docker push $(BRIDGE_REF)

clean:
	-docker rmi $(BRIDGE_REF) $(SIDECAR_REF)

$(CONFIG):
	@echo "config.json not found. Run 'make configure' first."
	@exit 1

# ─── Sidecar ──────────────────────────────────────────────────────────────────

sidecar-docs:
	cd sidecar && swag init

sidecar-build: $(CONFIG)
	docker build --platform=linux/amd64 -t $(SIDECAR_REF) sidecar

sidecar-push: sidecar-build
	docker push $(SIDECAR_REF)

# ─── Docker Compose ───────────────────────────────────────────────────────────

compose-up: $(CONFIG)
	BRIDGE_IMAGE=$(BRIDGE_REF) SIDECAR_IMAGE=$(SIDECAR_REF) \
	  docker compose up -d

compose-down:
	docker compose down

compose-logs:
	docker compose logs -f

compose-ps:
	docker compose ps
