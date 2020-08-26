docker:
	docker build . -t freeradius-v3

dockerc:
	docker run -p 1812-1813:1812-1813/udp -p 19815:1815/tcp --add-host jxradius.net:172.17.0.1 --name freeradius -t -d freeradius-v3 lfreemate

dockerx:
	docker run -p 1812-1813:1812-1813/udp -p 19815:1815/tcp --add-host jxradius.net:172.17.0.1 --name freeradius -t -d freeradius-v3 lfreemate -X

dockerrm:
	docker rm -f freeradius

dockersh:
	docker exec -it freeradius bash

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -ldflags '-s -w -extldflags "-static"' -o lfreemate freemate.go
	upx lfreemate
