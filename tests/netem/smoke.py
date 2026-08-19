#!/usr/bin/env python3
import json
import socket
import statistics
import struct
import threading
import time

PROFILES = {
    "relay-a": ("127.0.0.1", 5001),
    "relay-b": ("127.0.0.1", 5002),
}
PACKETS = 300
SEND_INTERVAL_SECONDS = 0.005
RECEIVE_GRACE_SECONDS = 2.0


def probe(name: str, relay_addr: tuple[str, int]) -> dict:
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
        "name": name,
        "sent": PACKETS,
        "received": received_count,
        "receive_ratio": received_count / PACKETS,
        "loss_ratio": 1.0 - (received_count / PACKETS),
        "median_rtt_ms": statistics.median(samples) if samples else 0.0,
        "p95_rtt_ms": samples[int((len(samples) - 1) * 0.95)] if samples else 0.0,
    }


def main() -> None:
    results = {}
    results_lock = threading.Lock()
    threads = []

    def run_profile(name, addr):
        result = probe(name, addr)
        with results_lock:
            results[name] = result

    for name, addr in PROFILES.items():
        thread = threading.Thread(target=run_profile, args=(name, addr))
        thread.start()
        threads.append(thread)

    for thread in threads:
        thread.join()

    print(json.dumps(results, indent=2, sort_keys=True))
    a = results["relay-a"]
    b = results["relay-b"]

    failures = []
    if a["receive_ratio"] < 0.98:
        failures.append(f"relay-a receive ratio too low: {a['receive_ratio']:.3f}")
    if b["receive_ratio"] < 0.65:
        failures.append(f"relay-b receive ratio too low: {b['receive_ratio']:.3f}")
    if b["loss_ratio"] < a["loss_ratio"] + 0.10:
        failures.append(
            f"loss profiles not distinct: a={a['loss_ratio']:.3f} b={b['loss_ratio']:.3f}"
        )
    if b["median_rtt_ms"] < a["median_rtt_ms"] + 120:
        failures.append(
            f"delay profiles not distinct: a={a['median_rtt_ms']:.1f}ms b={b['median_rtt_ms']:.1f}ms"
        )

    if failures:
        raise SystemExit("\n".join(failures))

    print("PASS: host traffic traversed both bidirectional netem relays with distinct loss/delay profiles")


if __name__ == "__main__":
    main()
