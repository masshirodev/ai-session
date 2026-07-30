GOCACHE=/tmp/ai-session-go-cache \
  go build -buildvcs=false \
  -o "$(go env GOPATH)/bin/ai" .

echo "Installed ai to $(go env GOPATH)/bin/ai"
