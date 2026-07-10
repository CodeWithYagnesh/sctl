run:
	go build -o obsctl && ./obsctl
install:
	go build -o obsctl && sudo cp obsctl /usr/local/bin