.PHONY: check check-verity check-vigil

check: check-verity check-vigil

check-verity:
	go -C verity test ./...
	python3 verity/codex-plugin/test_hook.py

check-vigil:
	go -C vigil test ./...
