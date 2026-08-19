#!/usr/bin/env python3
import json
import os
import socket
import statistics
import struct
import threading
import time

RELAY_PORT = int(os.environ.get("RELAY_PORT", "5000"))
TARGET_ADDR = ("127.0.0.1", RELAY_PORT)
PACKETS = 200
SEND_INTERVAL_SECONDS = 0.005
RECEIVE_GRACE_SECONDS = 2.0


def probe(relay_addr: tuple[str, int]) -> dict:
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    sock.bind(("127.0.0.1", 0))
    sock.settimeout(0.05)

    received = set()
    rtts_ms = []
    lock = threading.Lock()
    stop = threading.Event()

    def receive() -> None:
        while not stop.is_set():
            try:
                data, _ = sock.recvfrom(65535)
            except socket.timeout:
                continue
            except OSError:
                return
            if len(data) < 12:
                continue
            seq, sent_ns = struct.unpack("!IQ", data[:12])
            now_ns = time.monotonic_ns()
            with lock:
                if seq in received:
                    continue
                received.add(seq)
                rtts_ms.append((now_ns - sent_ns) / 1_000_000)

    receiver = threading.Thread(target=receive, daemon=True)
    receiver.start()

    payload = b"csp-netem-smoke"
    for seq in range(PACKETS):
        sent_ns = time.monotonic_ns()
        sock.sendto(struct.pack("!IQ", seq, sent_ns) + payload, relay_addr)
        time.sleep(SEND_INTERVAL_SECONDS)

    deadline = time.monotonic() + RECEIVE_GRACE_SECONDS
    while time.monotonic() < deadline:
        with lock:
            if len(received) >= PACKETS:
                break
        time.sleep(0.02)

    stop.set()
    receiver.join(timeout=0.2)
    sock.close()

    with lock:
        received_count = len(received)
        samples = sorted(rtts_ms)

    return {
        "target": f"{relay_addr[0]}:{relay_addr[1]}",
        "sent": PACKETS,
        "received": received_count,
        "receive_ratio": received_count / PACKETS,
        "loss_ratio": 1.0 - (received_count / PACKETS),
        "median_rtt_ms": statistics.median(samples) if samples else 0.0,
        "p95_rtt_ms": samples[int((len(samples) - 1) * 0.95)] if samples else 0.0,
    }


def main() -> None:
    result = probe(TARGET_ADDR)
    print(json.dumps(result, indent=2, sort_keys=True))

    failures = []
    if result["received"] == 0:
        failures.append("no packets received through netem relay")
    if result["median_rtt_ms"] < 60.0:
        failures.append(
            f"measured median RTT ({result['median_rtt_ms']:.1f}ms) is lower than expected netem delay (~100ms RTT)"
        )
    if result["receive_ratio"] < 0.60:
        failures.append(f"receive ratio too low: {result['receive_ratio']:.3f}")

    if failures:
        raise SystemExit("\n".join(failures))

    print("PASS: host traffic traversed bidirectional netem relay with configured impairment")


if __name__ == "__main__":
    main()
