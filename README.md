# The Terminator

[![Docker](https://img.shields.io/docker/v/fireflycons/terminator?style=plastic)](https://hub.docker.com/r/fireflycons/terminator)


Ever get the situation when you find some pods in your cluster just will not die? They've been stuck in the `Terminating` state for long past their grace period? This is for you!

I've found that there are sometimes pods that get in this state following a full cluster shutdown and restart - by full shutdown I mean stopping the instances that the cluster is running on, then when the instances are restarted, some pods (in my case `nfs-subdir-external-provisioner`) are never cleaned up from the previous shutdown and rematerialize in a `Terminating` state, even though the containers that back the pods are long since gone.

This tool works by scanning all pods in the cluster (static pods excluded) every so often for pods where the `deletionTimestamp` is longer ago than the current time minus the grace period. These pods are then force-terminated.

## Command line arguments

All the following are optional.

```
Usage: terminator

Flags:
  -h, --help                   Show context-sensitive help.
  -d, --dry-run                If set, do not delete anything
  -g, --grace-period=1h        Additional grace period added to that of the pod
                               in Go duration syntax, e.g 2m, 1h etc.
  -i, --interval=5m            Interval between scans of the cluster in Go
                               duration syntax, e.g 2m, 1h etc.
  -l, --log-level="info"       Sets the loglevel. Valid levels are debug, info,
                               warn, error
  -f, --log-format="logfmt"    Sets the log format. Valid formats are json and
                               logfmt
  -o, --log-output="stdout"    Sets the log output. Valid outputs are stdout and
                               stderr
```

## Installation

A helm chart is provided [here](./charts)

TODO: Add list of values

## Log output

Logs are output as several key-value pairs to make ingestion into log analysers like ElasticSearch easier. The logs may be emitted as plain text (default) or JSON by setting `--log-format`

| Key       | Value                                                  |
|-----------|--------------------------------------------------------|
| `level`   | Logging level (severity) of message.                   |
| `ts`      | Timestamp in ISO-8601 format.                          |
| `caller`  | Location in the code where the log message was raised. |
| `message` | The message text.                                      |

Most messages are emitted at `info` level. When a pod is terminated, messages about the termination are emitted at `warn` level.