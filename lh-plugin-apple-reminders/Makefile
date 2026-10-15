.PHONY: all build package clean

PLUGIN_ID := lh-plugin-apple-reminders
VERSION := 0.1.0
ARCH := $(shell uname -m)

ARTIFACT_DIR := dist/$(PLUGIN_ID)
EXECUTABLE := $(ARTIFACT_DIR)/$(PLUGIN_ID)
CACHE_DIR := $(CURDIR)/.cache

PACKAGE_FILE := dist/$(PLUGIN_ID)-$(VERSION)-darwin-$(ARCH).lhplugin
CANONICAL_PACKAGE := dist/$(PLUGIN_ID).lhplugin

all: build

# build produces a normal Lum Harbor Plugin artifact directory
# (plugin.json + matching executable), the same shape lh-plugin-base's own
# Makefile produces, so the existing Installer/Discovery pipeline needs no
# per-plugin special case.
build:
	mkdir -p $(CACHE_DIR)
	mkdir -p $(ARTIFACT_DIR)
	swiftc -O -module-cache-path $(CACHE_DIR) -Xcc -fmodules-cache-path=$(CACHE_DIR) -framework EventKit -framework Foundation Sources/$(PLUGIN_ID)/main.swift -o $(EXECUTABLE)
	cp plugin.json $(ARTIFACT_DIR)/plugin.json
	chmod +x $(EXECUTABLE)

# package produces canonical .lhplugin distribution archives under dist/
package: build
	rm -f $(PACKAGE_FILE) $(CANONICAL_PACKAGE)
	(cd $(ARTIFACT_DIR) && zip -r ../../$(PACKAGE_FILE) plugin.json $(PLUGIN_ID))
	cp $(PACKAGE_FILE) $(CANONICAL_PACKAGE)
	@echo "Packaged: $(PACKAGE_FILE)"
	@echo "Canonical: $(CANONICAL_PACKAGE)"

clean:
	rm -rf dist .build .cache
