.PHONY: dev restore weixin weixin-login

DATA_DIR ?= $(if $(SCAGENT_DATA_DIR),$(SCAGENT_DATA_DIR),data)

dev:
	./start.sh

weixin:
	WEIXIN_BRIDGE_ENABLED=1 ./start.sh

weixin-login:
	cd im/weixin && pnpm run login

restore:
	@echo "Resetting $(DATA_DIR)/state/store.db and $(DATA_DIR)/workspaces"
	rm -f "$(DATA_DIR)/state/store.db"
	rm -rf "$(DATA_DIR)/workspaces"
