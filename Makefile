build:
	mkdir -p functions
	cd cmd/phoneserver && go build -ldflags "-X main.Version=`git describe --tags`" -o ../../functions/phoneserver .

proto:
	protoc --proto_path=. --go_out=. \
		--go_opt=paths=source_relative \
		--go-grpc_out=require_unimplemented_servers=false:. \
		--go-grpc_opt=paths=source_relative \
		--proto_path=. *.proto
