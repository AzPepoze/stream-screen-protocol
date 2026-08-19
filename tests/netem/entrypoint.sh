#!/bin/sh
set -eu

iface="${NETEM_INTERFACE:-eth0}"
target="${TARGET_ADDR:-host.docker.internal:7700}"
listen="${LISTEN_ADDR:-:5000}"
preset="${NETEM_PRESET:-wifi}"

case "$preset" in
  1|lan|transparent)
    delay="0ms"; jitter="0ms"; loss="0%"; duplicate="0%"; rate=""
    profile_name="LAN / Transparent (0ms, 0% loss)"
    ;;
  2|broadband|fiber)
    delay="10ms"; jitter="1ms"; loss="0.1%"; duplicate="0%"; rate=""
    profile_name="Fast Broadband (10ms ± 1ms, 0.1% loss)"
    ;;
  3|wifi|standard|"")
    delay="20ms"; jitter="2ms"; loss="0.5%"; duplicate="0%"; rate=""
    profile_name="Standard Wi-Fi (20ms ± 2ms, 0.5% loss)"
    ;;
  4|mobile|4g|5g)
    delay="80ms"; jitter="15ms"; loss="3%"; duplicate="0%"; rate=""
    profile_name="Mobile 4G/5G (80ms ± 15ms, 3.0% loss)"
    ;;
  5|bad|bad-network|degraded)
    delay="150ms"; jitter="30ms"; loss="8%"; duplicate="0%"; rate=""
    profile_name="Degraded / Bad Network (150ms ± 30ms, 8.0% loss)"
    ;;
  6|extreme|stress)
    delay="250ms"; jitter="50ms"; loss="15%"; duplicate="0%"; rate="10mbit"
    profile_name="Extreme Stress Test (250ms ± 50ms, 15.0% loss, 10Mbps)"
    ;;
  7|custom|*)
    delay="${NETEM_DELAY:-0ms}"
    jitter="${NETEM_JITTER:-0ms}"
    loss="${NETEM_LOSS:-0%}"
    duplicate="${NETEM_DUPLICATE:-0%}"
    rate="${NETEM_RATE:-}"
    profile_name="Custom (delay=${delay}, jitter=${jitter}, loss=${loss}, rate=${rate:-unlimited})"
    ;;
esac

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

echo "=================================================="
echo " CSP Netem Relay Lab"
echo "=================================================="
echo " Active Preset    : ${profile_name}"
echo " Setup Guide:"
echo "   1. Server : make netem-server (listens on 0.0.0.0:7700)"
echo "   2. Client : make netem-client (connects to 127.0.0.1:${listen#:})"
echo " Flow        : Client -> Relay (${listen}) -> Server (${target})"
echo "=================================================="

if [ "$#" -gt 0 ]; then
  echo "netem: tc qdisc replace dev $iface root netem $*"
  tc qdisc replace dev "$iface" root netem "$@"
else
  echo "netem: no impairment configured; relay is transparent"
fi

tc qdisc show dev "$iface"
exec /usr/local/bin/netem-relay
