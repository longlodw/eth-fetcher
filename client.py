#!/usr/bin/env python3
import argparse
import requests
import sys

def cmd_request(args):
    r = requests.post(f"{args.server}/request?start={args.start}&end={args.end}")
    r.raise_for_status()
    print(r.json())

def cmd_status(args):
    r = requests.get(f"{args.server}/status/{args.jobid}")
    r.raise_for_status()
    print(r.json())

def cmd_stop(args):
    r = requests.get(f"{args.server}/stop/{args.jobid}")
    r.raise_for_status()
    print(r.text)

def cmd_download(args):
    r = requests.get(f"{args.server}/download/{args.jobid}")
    if r.status_code != 200:
        print("Error:", r.text)
        sys.exit(1)
    with open(args.output, "wb") as f:
        f.write(r.content)
    print(f"Saved to {args.output}")

def cmd_list(args):
    r = requests.get(f"{args.server}/jobs")
    r.raise_for_status()
    for jobid in r.json():
        print(jobid)

def main():
    parser = argparse.ArgumentParser(description="Ethereum Fetcher CLI Client")
    parser.add_argument(
        "--server", default="http://localhost:8080",
        help="Base URL of the server (default: http://localhost:8080)"
    )
    sub = parser.add_subparsers(title="commands")

    p_req = sub.add_parser("request", help="Submit a new job")
    p_req.add_argument("start", type=int, help="Start block")
    p_req.add_argument("end", type=int, help="End block")
    p_req.set_defaults(func=cmd_request)

    p_stat = sub.add_parser("status", help="Check job status")
    p_stat.add_argument("jobid", help="Job ID")
    p_stat.set_defaults(func=cmd_status)

    p_stop = sub.add_parser("stop", help="Stop a running job")
    p_stop.add_argument("jobid", help="Job ID")
    p_stop.set_defaults(func=cmd_stop)

    p_down = sub.add_parser("download", help="Download job CSV")
    p_down.add_argument("jobid", help="Job ID")
    p_down.add_argument("output", help="Output CSV file")
    p_down.set_defaults(func=cmd_download)

    p_list = sub.add_parser("list", help="List all job IDs")
    p_list.set_defaults(func=cmd_list)

    args = parser.parse_args()
    if hasattr(args, "func"):
        args.func(args)
    else:
        parser.print_help()

if __name__ == "__main__":
    main()

