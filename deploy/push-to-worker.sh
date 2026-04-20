# push to worker
#
# by 3n3a

GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o mediforge-worker ./cmd/mediforge-worker
sudo cp ./mediforge-worker /usr/local/bin/
