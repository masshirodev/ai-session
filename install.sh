GOCACHE=/tmp/ai-session-go-cache \
  go build -buildvcs=false \
  -o "$(go env GOPATH)/bin/ai" .
