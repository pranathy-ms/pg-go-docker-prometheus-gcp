# syntax=docker/dockerfile:1
FROM golang:1.21-alpine
ENV PORT 8080
ENV HOSTDIR 0.0.0.0
ENV GITHUB_TOKEN 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

EXPOSE 8080
WORKDIR /app
COPY go.mod ./
COPY go.sum ./
RUN go mod tidy
COPY . ./
RUN go build -o /main
CMD [ "/main" ]