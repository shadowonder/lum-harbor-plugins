.PHONY: all build package test clean

PLUGIN_ID := lh-plugin-ai-runtime
VERSION := 0.1.0
ARCH := $(shell uname -m)

ARTIFACT_DIR := dist/$(PLUGIN_ID)
EXECUTABLE := $(ARTIFACT_DIR)/$(PLUGIN_ID)

PACKAGE_FILE := dist/$(PLUGIN_ID)-$(VERSION)-darwin-$(ARCH).lhplugin
CANONICAL_PACKAGE := dist/$(PLUGIN_ID).lhplugin

all: build

build:
	mkdir -p $(ARTIFACT_DIR)
	go build -trimpath -o $(EXECUTABLE) .
	cp plugin.json $(ARTIFACT_DIR)/plugin.json
	chmod +x $(EXECUTABLE)

package: build
	rm -f $(PACKAGE_FILE) $(CANONICAL_PACKAGE)
	(cd $(ARTIFACT_DIR) && zip -r ../../$(PACKAGE_FILE) plugin.json $(PLUGIN_ID))
	cp $(PACKAGE_FILE) $(CANONICAL_PACKAGE)
	@echo "Packaged: $(PACKAGE_FILE)"
	@echo "Canonical: $(CANONICAL_PACKAGE)"

test:
	go test -v ./...

clean:
	rm -rf dist
	rm -f $(PLUGIN_ID)

