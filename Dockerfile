FROM golang:1.25-alpine AS build

WORKDIR /src
COPY . .
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/gitea-chatgpt-mcp ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/gitea-chatgpt-mcp /gitea-chatgpt-mcp
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/gitea-chatgpt-mcp"]
