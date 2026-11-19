#!/usr/bin/env python3
"""
Webhook trigger script for the workflow demo.

This script:
  1. Authenticates via `/oauth/token`
  2. Reads the credential returned in `result.api_key`
  3. Builds `Authorization: Basic <base64(login:api_key)>`
  4. Calls `POST /wh/<topic>`

It does not create workflows or workflow actions.
Use it only after the demo workflow has already been seeded in the backend.

Usage:
  python test_workflow_demo.py
  python test_workflow_demo.py --base-url http://localhost:8180 --topic demo
  python test_workflow_demo.py --payload-file /tmp/demo_payload.json
"""

import argparse
import base64
import json
import sys

import requests


DEFAULT_BASE_URL = "http://localhost:8180"
DEFAULT_DB = "goetl"
DEFAULT_TOPIC = "demo"
DEFAULT_TIMEOUT = 30
DEFAULT_LOGIN = "admin"
DEFAULT_PASSWORD = "admin"

SAMPLE_PAYLOAD = {
    "name": "test-txn-001",
    "txn_date": 1757854865.541272,
    "txn_type": "ibft",
    "txn_status": "created",
    "txn_amount": 10000,
    "is_debit": False,
    "txn_fee": 0,
    "id": "txn-test-001",
    "currency": "VND",
    "narrative": "123 bill",
    "bill_id": "bill-001",
    "acc_holder_zid": "",
    "proxy_zid": "",
    "company_ref_code": "",
    "proxy_name": "",
    "frm_acc_no": "0123456789",
    "frm_acc_name": "NGUYEN VAN A",
    "frm_acc_bincode": "970415",
    "frm_acc_bankname": "VietinBank",
    "to_acc_va": "",
    "to_acc_no": "9876543210",
    "to_acc_name": "TRAN THI B",
    "to_acc_bincode": "970436",
    "to_acc_bankname": "Vietcombank",
    "bank_txn_id": "BANK-001",
    "napas_txn_id": "11",
}


def step(msg: str):
    print(f"\n{'=' * 60}")
    print(f"  {msg}")
    print("=" * 60)


def ok(msg: str):
    print(f"  [OK]  {msg}")


def fail(msg: str):
    print(f"  [FAIL] {msg}")
    sys.exit(1)


def load_payload(payload_file: str | None) -> dict:
    if not payload_file:
        return dict(SAMPLE_PAYLOAD)

    try:
        with open(payload_file, "r", encoding="utf-8") as handle:
            payload = json.load(handle)
    except FileNotFoundError:
        fail(f"Payload file not found: {payload_file}")
    except json.JSONDecodeError as exc:
        fail(f"Payload file is not valid JSON: {exc}")

    if not isinstance(payload, dict):
        fail("Payload file must contain a JSON object")
    return payload


def authenticate(session: requests.Session, base_url: str, db: str, login: str, password: str, timeout: int) -> tuple[int, str]:
    url = f"{base_url.rstrip('/')}/oauth/token"
    body = {
        "params": {
            "db": db,
            "login": login,
            "password": password,
            "context": {},
        }
    }
    response = session.post(url, json=body, timeout=timeout)
    response.raise_for_status()

    data = response.json()
    if "error" in data and data["error"]:
        fail(f"authenticate: {data['error']}")

    result = data.get("result", {})
    uid = result.get("uid")
    if not uid:
        fail(f"authenticate: no uid in response: {data}")
    api_key = result.get("api_key")
    if not api_key:
        fail(f"authenticate: no api_key in response: {data}")
    return uid, str(api_key)


def build_basic_auth_header(login: str, api_key: str) -> str:
    encoded = base64.b64encode(f"{login}:{api_key}".encode("utf-8")).decode("ascii")
    return f"Basic {encoded}"


def post_webhook(
    session: requests.Session,
    base_url: str,
    db: str,
    topic: str,
    payload: dict,
    authorization_header: str,
    timeout: int,
):
    url = f"{base_url.rstrip('/')}/wh/{topic.strip('/')}"
    headers = {
        "Content-Type": "application/json",
        "X-Database-Name": db,
        "Authorization": authorization_header,
    }
    return session.post(url, json=payload, headers=headers, timeout=timeout)


def main():
    parser = argparse.ArgumentParser(description="Trigger workflow demo via /wh/<topic>")
    parser.add_argument("--base-url", default=DEFAULT_BASE_URL, help="Webhook base URL")
    parser.add_argument("--db", default=DEFAULT_DB, help="Database name header")
    parser.add_argument("--topic", default=DEFAULT_TOPIC, help="Workflow topic")
    parser.add_argument("--timeout", type=int, default=DEFAULT_TIMEOUT, help="HTTP timeout in seconds")
    parser.add_argument("--login", default=DEFAULT_LOGIN, help="Login for /oauth/token")
    parser.add_argument("--password", default=DEFAULT_PASSWORD, help="Password for /oauth/token")
    parser.add_argument("--payload-file", help="Path to a JSON file containing the webhook payload")
    args = parser.parse_args()

    payload = load_payload(args.payload_file)
    session = requests.Session()
    session.headers.update({"Content-Type": "application/json"})

    print("\nWorkflow Webhook Trigger")
    print(f"  Base URL : {args.base_url}")
    print(f"  Database : {args.db}")
    print(f"  Topic    : {args.topic}")

    step("Authenticate")
    try:
        uid, api_key = authenticate(
            session,
            args.base_url,
            args.db,
            args.login,
            args.password,
            args.timeout,
        )
    except requests.RequestException as exc:
        fail(f"Authenticate request failed: {exc}")
    ok(f"Authenticated as uid={uid} login={args.login}")
    authorization_header = build_basic_auth_header(args.login, api_key)

    step(f"POST /wh/{args.topic}")
    print(f"  Payload: {json.dumps(payload, indent=4)}")

    try:
        response = post_webhook(
            session,
            args.base_url,
            args.db,
            args.topic,
            payload,
            authorization_header,
            args.timeout,
        )
    except requests.RequestException as exc:
        fail(f"Webhook request failed: {exc}")

    print(f"\n  HTTP status : {response.status_code}")
    try:
        print(f"  Response    : {json.dumps(response.json(), indent=4)}")
    except ValueError:
        print(f"  Response    : {response.text}")

    if response.ok:
        ok("Webhook request completed")
        return

    fail(f"Webhook trigger returned HTTP {response.status_code}")


if __name__ == "__main__":
    main()
