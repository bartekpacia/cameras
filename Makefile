all: cameras

cameras: main.go byte_sizes.go
	go build -o cameras main.go byte_sizes.go

clean:
	rm -f ./cameras
