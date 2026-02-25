CONFIG = build-config.json

ifneq ($(wildcard $(CONFIG)),)
BASE_IMAGE := $(shell python3 -c "import json;c=json.load(open('$(CONFIG)'));s=c['source'];print(s['registry']+'/'+s['image']+':'+s['tag'])")
REGISTRY   := $(shell python3 -c "import json;print(json.load(open('$(CONFIG)'))['target']['registry'])")
IMAGE_NAME := $(shell python3 -c "import json;print(json.load(open('$(CONFIG)'))['target']['image'])")
IMAGE_TAG  := $(shell python3 -c "import json;print(json.load(open('$(CONFIG)'))['target']['tag'])")
endif

IMAGE_REF = $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)

.PHONY: configure build push clean

configure:
	python3 build/configure.py

build: $(CONFIG)
	docker build --platform=linux/amd64 --build-arg BASE_IMAGE=$(BASE_IMAGE) -t $(IMAGE_REF) build

push: build
	docker push $(IMAGE_REF)

clean:
	-docker rmi $(IMAGE_REF)

$(CONFIG):
	@echo "build-config.json not found. Run 'make configure' first."
	@exit 1

# ─── Sidecar ──────────────────────────────────────────────────────────────────

SIDECAR_CONFIG = sidecar-config.json

ifneq ($(wildcard $(SIDECAR_CONFIG)),)
SIDECAR_REGISTRY  := $(shell python3 -c "import json;print(json.load(open('$(SIDECAR_CONFIG)'))['target']['registry'])")
SIDECAR_IMAGE     := $(shell python3 -c "import json;print(json.load(open('$(SIDECAR_CONFIG)'))['target']['image'])")
SIDECAR_TAG       := $(shell python3 -c "import json;print(json.load(open('$(SIDECAR_CONFIG)'))['target']['tag'])")
endif

SIDECAR_REF = $(SIDECAR_REGISTRY)/$(SIDECAR_IMAGE):$(SIDECAR_TAG)

.PHONY: sidecar-configure sidecar-docs sidecar-build sidecar-push

sidecar-configure:
	python3 sidecar/configure.py

sidecar-docs:
	cd sidecar && swag init

sidecar-build: $(SIDECAR_CONFIG)
	docker build --platform=linux/amd64 -t $(SIDECAR_REF) sidecar

sidecar-push: sidecar-build
	docker push $(SIDECAR_REF)

$(SIDECAR_CONFIG):
	@echo "sidecar-config.json not found. Run 'make sidecar-configure' first."
	@exit 1
