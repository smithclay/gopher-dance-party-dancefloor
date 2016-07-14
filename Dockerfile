FROM golang:1.6-alpine

ADD . /go/src/app

RUN apk add --no-cache git

# Install dependencies
RUN go get github.com/newrelic/go-agent
RUN go get github.com/garyburd/redigo/redis
RUN go install app

CMD ["app"]
