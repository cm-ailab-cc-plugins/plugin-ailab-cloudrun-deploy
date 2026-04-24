FROM golang:1.22 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o server .

FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=builder /app/server .
EXPOSE 8080
ENV PORT=8080
CMD ["/app/server"]
