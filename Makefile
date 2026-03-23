.PHONY: dev restore weixin weixin-login weixin-logout

DATA_DIR ?= $(if $(SCAGENT_DATA_DIR),$(SCAGENT_DATA_DIR),data)

dev:
	./start.sh

weixin:
	WEIXIN_BRIDGE_ENABLED=1 ./start.sh

weixin-login:
	go run ./cmd/scagent -weixin-login

weixin-logout:
	go run ./cmd/scagent -weixin-logout

restore:
	go run ./cmd/scagent reset --all --data-dir "$(DATA_DIR)"
