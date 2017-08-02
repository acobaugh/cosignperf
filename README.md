cosignperf is a simple tool designed to send a series of commands to cosignd[1] in parallel, up to *n* iterations, and print out various aggregate statistics about response time, number of errors, etc.

[1] http://weblogin.org/

## Installation
`$ go get github.com/cobaugh/cosignperf`

## Build
```
$ go get -d ./...
$ go build
```

## Usage

## TODO
* quiet/verbose output
* generic cosign comm handler function to simplify some of the nested if/else logic
* delays between jobs/commands
