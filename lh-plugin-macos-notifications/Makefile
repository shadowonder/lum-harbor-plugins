.PHONY: all build test package clean

PLUGIN_ID := lh-plugin-macos-notifications
VERSION := 0.1.0
ARCH := $(shell uname -m)

ARTIFACT_DIR := dist/$(PLUGIN_ID)
BUNDLE_DIR := $(ARTIFACT_DIR)/$(PLUGIN_ID).app
CONTENTS_DIR := $(BUNDLE_DIR)/Contents
MACOS_DIR := $(CONTENTS_DIR)/MacOS
EXECUTABLE := $(MACOS_DIR)/$(PLUGIN_ID)
CACHE_DIR := $(CURDIR)/.cache
SWIFT_BUILD_DIR := $(CURDIR)/.build-tmp
RESOURCES_DIR := $(CONTENTS_DIR)/Resources
ICONSET_DIR := $(SWIFT_BUILD_DIR)/LumHarborAppIcon.iconset
APP_ICON_SOURCE := ../lum-harbor-notifier/build/appicon.png
SOSUMI_SOUND_SOURCE := /System/Library/Sounds/Sosumi.aiff

PACKAGE_FILE := dist/$(PLUGIN_ID)-$(VERSION)-darwin-$(ARCH).lhplugin
CANONICAL_PACKAGE := dist/$(PLUGIN_ID).lhplugin

all: build

test:
	swift run --scratch-path $(SWIFT_BUILD_DIR) notification-support-tests

# build produces a Lum Harbor Plugin artifact directory containing plugin.json
# and a minimal application bundle satisfying macOS notification identity requirements.
build:
	mkdir -p $(CACHE_DIR)
	mkdir -p $(MACOS_DIR)
	mkdir -p $(RESOURCES_DIR)
	swift build -c release --scratch-path $(SWIFT_BUILD_DIR) --product $(PLUGIN_ID)
	cp $(SWIFT_BUILD_DIR)/release/$(PLUGIN_ID) $(EXECUTABLE)
	mkdir -p $(ICONSET_DIR)
	sips -z 16 16 $(APP_ICON_SOURCE) --out $(ICONSET_DIR)/icon_16x16.png >/dev/null
	sips -z 32 32 $(APP_ICON_SOURCE) --out $(ICONSET_DIR)/icon_16x16@2x.png >/dev/null
	sips -z 32 32 $(APP_ICON_SOURCE) --out $(ICONSET_DIR)/icon_32x32.png >/dev/null
	sips -z 64 64 $(APP_ICON_SOURCE) --out $(ICONSET_DIR)/icon_32x32@2x.png >/dev/null
	sips -z 128 128 $(APP_ICON_SOURCE) --out $(ICONSET_DIR)/icon_128x128.png >/dev/null
	sips -z 256 256 $(APP_ICON_SOURCE) --out $(ICONSET_DIR)/icon_128x128@2x.png >/dev/null
	sips -z 256 256 $(APP_ICON_SOURCE) --out $(ICONSET_DIR)/icon_256x256.png >/dev/null
	sips -z 512 512 $(APP_ICON_SOURCE) --out $(ICONSET_DIR)/icon_256x256@2x.png >/dev/null
	sips -z 512 512 $(APP_ICON_SOURCE) --out $(ICONSET_DIR)/icon_512x512.png >/dev/null
	cp $(APP_ICON_SOURCE) $(ICONSET_DIR)/icon_512x512@2x.png
	iconutil -c icns $(ICONSET_DIR) -o $(RESOURCES_DIR)/LumHarborAppIcon.icns
	test -f $(SOSUMI_SOUND_SOURCE)
	cp $(SOSUMI_SOUND_SOURCE) $(RESOURCES_DIR)/Sosumi.aiff
	cp Info.plist $(CONTENTS_DIR)/Info.plist
	cp plugin.json $(ARTIFACT_DIR)/plugin.json
	chmod +x $(EXECUTABLE)
	codesign --force --deep --sign - $(BUNDLE_DIR)

# package produces canonical .lhplugin distribution archives under dist/
package: build
	rm -f $(PACKAGE_FILE) $(CANONICAL_PACKAGE)
	(cd $(ARTIFACT_DIR) && zip -r ../../$(PACKAGE_FILE) plugin.json $(PLUGIN_ID).app)
	cp $(PACKAGE_FILE) $(CANONICAL_PACKAGE)
	@echo "Packaged: $(PACKAGE_FILE)"
	@echo "Canonical: $(CANONICAL_PACKAGE)"

clean:
	rm -rf dist .cache .build-tmp
