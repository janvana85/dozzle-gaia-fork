#!/usr/bin/env python3
import argparse
import datetime as dt
import json
import os
import pathlib
import sqlite3
import subprocess
from collections import defaultdict


def sh(*args: str) -> str:
    return subprocess.check_output(args, text=True).strip()


def docker_inspect(container_ids: list[str]) -> list[dict]:
    if not container_ids:
        return []
    raw = sh("docker", "inspect", *container_ids)
    return json.loads(raw)


def ensure_schema(conn: sqlite3.Connection) -> None:
    conn.executescript(
        """
        CREATE TABLE IF NOT EXISTS log_chunks (
          host TEXT NOT NULL,
          container_id TEXT NOT NULL,
          identity TEXT NOT NULL DEFAULT '',
          day TEXT NOT NULL,
          path TEXT NOT NULL,
          first_ts INTEGER NOT NULL,
          last_ts INTEGER NOT NULL,
          lines INTEGER NOT NULL DEFAULT 0,
          updated_at INTEGER NOT NULL,
          PRIMARY KEY (host, container_id, day)
        );

        CREATE TABLE IF NOT EXISTS cached_containers (
          host TEXT NOT NULL,
          container_id TEXT NOT NULL,
          identity TEXT NOT NULL DEFAULT '',
          state TEXT NOT NULL DEFAULT '',
          finished_at INTEGER NOT NULL DEFAULT 0,
          payload TEXT NOT NULL,
          updated_at INTEGER NOT NULL,
          PRIMARY KEY (host, container_id)
        );

        CREATE INDEX IF NOT EXISTS idx_log_chunks_lookup ON log_chunks(host, container_id, day, first_ts, last_ts);
        CREATE INDEX IF NOT EXISTS idx_log_chunks_identity_lookup ON log_chunks(host, identity, day, first_ts, last_ts);
        CREATE INDEX IF NOT EXISTS idx_cached_containers_host ON cached_containers(host, updated_at);
        CREATE INDEX IF NOT EXISTS idx_cached_containers_identity ON cached_containers(host, identity);
        """
    )
    conn.commit()


def event_payload(message: str, raw: str, timestamp_ms: int, event_id: int, container_id: str, level: str, stream: str):
    return {
        "t": "single",
        "m": message,
        "rm": raw,
        "ts": timestamp_ms,
        "id": event_id,
        "l": level,
        "s": stream,
        "c": container_id,
    }


def complex_payload(container_name: str, host_id: str, timestamp_ms: int, event_id: int, container_id: str):
    message = {
        "service": container_name,
        "host": host_id,
        "event": "stress-json",
        "marker": "EDGE_CACHE_JSON",
        "eventId": event_id,
    }
    raw = json.dumps(message, separators=(",", ":"))
    return {
        "t": "single",
        "m": message,
        "rm": raw,
        "ts": timestamp_ms,
        "id": event_id,
        "l": "info",
        "s": "stdout",
        "c": container_id,
    }


def build_container_payload(meta: dict, host_id: str) -> dict:
    created = meta["Created"]
    state = meta["State"]
    labels = meta["Config"].get("Labels") or {}
    short_id = meta["Id"][:12]
    return {
        "id": short_id,
        "name": meta["Name"].lstrip("/"),
        "image": meta["Config"]["Image"],
        "command": " ".join(meta["Config"].get("Cmd") or []),
        "created": created,
        "startedAt": state.get("StartedAt") or created,
        "finishedAt": state.get("FinishedAt") or "0001-01-01T00:00:00Z",
        "state": "running",
        "host": host_id,
        "labels": labels,
        "memoryLimit": 33554432,
        "cpuLimit": 0.03,
        "group": labels.get("dev.dozzle.group", ""),
        "mounts": [],
        "mountStats": {},
    }


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--project", required=True)
    parser.add_argument("--data-dir", required=True)
    parser.add_argument("--days", type=int, default=4)
    parser.add_argument("--lines-per-day", type=int, default=250)
    parser.add_argument("--include-expired", type=int, default=1)
    parser.add_argument("--db-path-prefix", default="/data/cache")
    args = parser.parse_args()

    data_dir = pathlib.Path(args.data_dir)
    cache_dir = data_dir / "cache"
    cache_dir.mkdir(parents=True, exist_ok=True)

    container_ids_raw = sh(
        "docker",
        "ps",
        "-q",
        "--filter",
        f"label=dozzle.sim={args.project}",
        "--filter",
        "label=dozzle.simhost",
    )
    container_ids = [line for line in container_ids_raw.splitlines() if line]
    inspected = docker_inspect(container_ids)

    now = dt.datetime.now(dt.timezone.utc)
    conn = sqlite3.connect(cache_dir / "index.sqlite")
    ensure_schema(conn)

    manifest: dict[str, object] = {
        "project": args.project,
        "generatedAt": now.isoformat(),
        "days": args.days,
        "linesPerDay": args.lines_per_day,
        "containers": [],
    }

    for meta in inspected:
        labels = meta["Config"].get("Labels") or {}
        sim_host = labels["dozzle.simhost"]
        host_id = f"sim-{sim_host}"
        short_id = meta["Id"][:12]
        container_id = short_id
        container_name = meta["Name"].lstrip("/")
        payload = build_container_payload(meta, host_id)
        payload_json = json.dumps(payload, separators=(",", ":"))
        updated_at = int(now.timestamp())

        conn.execute(
            """
            INSERT OR REPLACE INTO cached_containers(host, container_id, identity, state, finished_at, payload, updated_at)
            VALUES(?, ?, '', 'running', 0, ?, ?)
            """,
            (host_id, container_id, payload_json, updated_at),
        )

        manifest["containers"].append(
            {
                "host": host_id,
                "id": container_id,
                "name": container_name,
                "searchTerm": "EDGE_STRESS_MATCH",
            }
        )

        for day_offset in range(args.days):
            day = (now - dt.timedelta(days=day_offset)).date()
            dir_path = cache_dir / host_id / short_id
            dir_path.mkdir(parents=True, exist_ok=True)
            file_path = dir_path / f"{day.isoformat()}.ndjson"

            first_ts = None
            last_ts = None
            lines_written = 0
            with file_path.open("w", encoding="utf-8") as fh:
                for idx in range(args.lines_per_day):
                    ts = dt.datetime.combine(day, dt.time.min, tzinfo=dt.timezone.utc) + dt.timedelta(
                        seconds=(idx * 86400) / max(args.lines_per_day, 1)
                    )
                    timestamp_ms = int(ts.timestamp() * 1000)
                    event_id = day_offset * 100000 + idx + 1
                    if idx % 25 == 0:
                        payload_obj = complex_payload(container_name, host_id, timestamp_ms, event_id, container_id)
                    else:
                        marker = "EDGE_STRESS_MATCH" if idx % 17 == 0 else "noise"
                        level = "error" if idx % 41 == 0 else ("warn" if idx % 13 == 0 else "info")
                        raw = (
                            f"{level.upper()} cache-stress host={host_id} container={container_name} "
                            f"marker={marker} day={day.isoformat()} seq={idx}"
                        )
                        payload_obj = event_payload(
                            raw,
                            raw,
                            timestamp_ms,
                            event_id,
                            container_id,
                            level,
                            "stderr" if idx % 29 == 0 else "stdout",
                        )
                    fh.write(json.dumps(payload_obj, separators=(",", ":")) + "\n")
                    first_ts = timestamp_ms if first_ts is None else min(first_ts, timestamp_ms)
                    last_ts = timestamp_ms if last_ts is None else max(last_ts, timestamp_ms)
                    lines_written += 1

            conn.execute(
                """
                INSERT OR REPLACE INTO log_chunks(host, container_id, identity, day, path, first_ts, last_ts, lines, updated_at)
                VALUES(?, ?, '', ?, ?, ?, ?, ?, ?)
                """,
                (
                    host_id,
                    container_id,
                    day.isoformat(),
                    f"{args.db_path_prefix}/{host_id}/{short_id}/{day.isoformat()}.ndjson",
                    first_ts,
                    last_ts,
                    lines_written,
                    updated_at,
                ),
            )

        if args.include_expired:
            expired_day = (now - dt.timedelta(days=args.days + 3)).date()
            dir_path = cache_dir / host_id / short_id
            file_path = dir_path / f"{expired_day.isoformat()}.ndjson"
            expired_ts = int(
                dt.datetime.combine(expired_day, dt.time(hour=12), tzinfo=dt.timezone.utc).timestamp() * 1000
            )
            expired = event_payload(
                f"INFO expired-marker host={host_id} container={container_name} marker=EXPIRED_CACHE_MARKER",
                f"INFO expired-marker host={host_id} container={container_name} marker=EXPIRED_CACHE_MARKER",
                expired_ts,
                999999999,
                container_id,
                "info",
                "stdout",
            )
            with file_path.open("w", encoding="utf-8") as fh:
                fh.write(json.dumps(expired, separators=(",", ":")) + "\n")
            conn.execute(
                """
                INSERT OR REPLACE INTO log_chunks(host, container_id, identity, day, path, first_ts, last_ts, lines, updated_at)
                VALUES(?, ?, '', ?, ?, ?, ?, 1, ?)
                """,
                (
                    host_id,
                    container_id,
                    expired_day.isoformat(),
                    f"{args.db_path_prefix}/{host_id}/{short_id}/{expired_day.isoformat()}.ndjson",
                    expired_ts,
                    expired_ts,
                    updated_at,
                ),
            )

    conn.commit()
    conn.close()
    (data_dir / "stress-manifest.json").write_text(json.dumps(manifest, indent=2), encoding="utf-8")
    print(f"seeded cache for {len(inspected)} containers into {cache_dir}")


if __name__ == "__main__":
    main()
