BINARY_NAME := cleancode
INSTALL_DIR := /usr/local/bin

.PHONY: build install uninstall clean

build:
	CGO_ENABLED=1 go build -o $(BINARY_NAME) ./cmd/cleancode/

install: build
	@echo "Installing $(BINARY_NAME) to $(INSTALL_DIR)..."
	@cp $(BINARY_NAME) $(INSTALL_DIR)/$(BINARY_NAME) 2>/dev/null || sudo cp $(BINARY_NAME) $(INSTALL_DIR)/$(BINARY_NAME)
	@echo "Done! Run 'cleancode --help' to get started."

uninstall:
	@echo "Removing $(BINARY_NAME) from $(INSTALL_DIR)..."
	@rm -f $(INSTALL_DIR)/$(BINARY_NAME) 2>/dev/null || sudo rm -f $(INSTALL_DIR)/$(BINARY_NAME)
	@echo "Done."

clean:
	@rm -f $(BINARY_NAME)
