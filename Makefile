docker:
	docker build . -t freeradius-v3

dockerc:
	docker run -p 18912-18913:18912-18913/udp --name freeradius -t -d freeradius-v3

dockerrm:
	docker rm -f freeradius

dockersh:
	docker exec -it freeradius bash