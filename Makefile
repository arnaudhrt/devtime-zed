# Cross-compile devtime-ls for all Zed-supported platforms
# Usage:
#   make release      — build + zip all targets into dist/
#   make build-local  — build for current platform only

BINARY = devtime-ls
DIST   = dist

# target triple mapping: GOOS/GOARCH → Rust-style triple
TARGETS = \
	darwin/arm64/devtime-ls-aarch64-apple-darwin \
	darwin/amd64/devtime-ls-x86_64-apple-darwin \
	linux/arm64/devtime-ls-aarch64-unknown-linux-gnu \
	linux/amd64/devtime-ls-x86_64-unknown-linux-gnu \
	windows/amd64/devtime-ls-x86_64-pc-windows-msvc

.PHONY: release build-local clean

release: clean
	@mkdir -p $(DIST)
	@for target in $(TARGETS); do \
		os=$$(echo $$target | cut -d/ -f1); \
		arch=$$(echo $$target | cut -d/ -f2); \
		triple=$$(echo $$target | cut -d/ -f3); \
		ext=""; \
		if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		echo "Building $$triple ($$os/$$arch)..."; \
		cd devtime-ls && CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go build -ldflags="-s -w" -o ../$(DIST)/$(BINARY)$$ext . && cd ..; \
		cd $(DIST) && zip -q $${triple}.zip $(BINARY)$$ext && rm $(BINARY)$$ext && cd ..; \
	done
	@echo ""
	@echo "Assets in $(DIST)/:"
	@ls -lh $(DIST)/*.zip

build-local:
	@cd devtime-ls && go build -o $(BINARY) .
	@echo "Built devtime-ls/$(BINARY)"

clean:
	@rm -rf $(DIST)
