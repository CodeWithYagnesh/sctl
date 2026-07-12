run:
	go build -o sctl && ./sctl
install:
	go build -o sctl && sudo cp sctl /usr/local/bin
help:
	go build -o sctl && ./sctl --help
