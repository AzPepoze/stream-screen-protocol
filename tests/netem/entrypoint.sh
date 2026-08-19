#!/bin/sh
set -eu

iface="${NETEM_INTERFACE:-eth0}"
delay="${NETEM_DELAY:-0ms}"
jitter="${NETEM_JITTER:-0ms}"
loss="${NETEM_LOSS:-0%}"
duplicate="${NETEM_DUPLICATE:-0%}"
rate="${NETEM_RATE:-}"

set --

if [ "$delay" != "0" ] && [ "$delay" != "0ms" ]; then
  set -- "$@" delay "$delay"
  if [ "$jitter" != "0" ] && [ "$jitter" != "0ms" ]; then
    set -- "$@" "$jitter" distribution normal
  fi
fi

if [ "$loss" != "0" ] && [ "$loss" != "0%" ]; then
  set -- "$@" loss random "$loss"
fi

if [ "$duplicate" != "0" ] && [ "$duplicate" != "0%" ]; then
  set -- "$@" duplicate "$duplicate"
fi

if [ -n "$rate" ]; then
  set -- "$@" rate "$rate"
fi

if [ "$#" -gt 0 ]; then
  echo "netem: tc qdisc replace dev $iface root netem $*"
  tc qdisc replace dev "$iface" root netem "$@"
else
  echo "netem: no impairment configured; relay is transparent"
fi

tc qdisc show dev "$iface"
exec /usr/local/bin/netem-relay
