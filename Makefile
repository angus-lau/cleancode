BINARY_NAME := cleancode
USER_INSTALL_DIR := $(HOME)/.local/bin
SYSTEM_INSTALL_DIR := /usr/local/bin

.PHONY: build install uninstall clean

build:
	CGO_ENABLED=1 go build -o $(BINARY_NAME) ./cmd/cleancode/

install: build
	@mkdir -p $(USER_INSTALL_DIR)
	@cp $(BINARY_NAME) $(USER_INSTALL_DIR)/$(BINARY_NAME)
	@if echo "$$PATH" | tr ':' '\n' | grep -qx "$(USER_INSTALL_DIR)"; then \
		echo "Installed $(BINARY_NAME) to $(USER_INSTALL_DIR)"; \
	else \
		echo "Installed $(BINARY_NAME) to $(USER_INSTALL_DIR)"; \
		echo ""; \
		echo "  Add this to your shell profile (~/.zshrc or ~/.bashrc):"; \
		echo "    export PATH=\"$(USER_INSTALL_DIR):\$$PATH\""; \
		echo ""; \
	fi
	@echo "Done! Run 'cleancode --help' to get started."

uninstall:
	@rm -f $(USER_INSTALL_DIR)/$(BINARY_NAME) 2>/dev/null
	@rm -f $(SYSTEM_INSTALL_DIR)/$(BINARY_NAME) 2>/dev/null || sudo rm -f $(SYSTEM_INSTALL_DIR)/$(BINARY_NAME) 2>/dev/null
	@echo "Done."

clean:
	@rm -f $(BINARY_NAME)
