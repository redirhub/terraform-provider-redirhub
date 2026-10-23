default: build

build:
	go build -o terraform-provider-redirhub .

test:
	go test ./...

# Runs TestAccLive against a real workspace: REDIRHUB_API_TOKEN (and REDIRHUB_API_URL for a local API).
testacc:
	TF_ACC=1 go test ./... -run TestAccLive -v -timeout 20m

docs:
	go generate ./...

.PHONY: build test testacc docs
