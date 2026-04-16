#!/bin/sh
# Runs inside LocalStack on startup — creates the development S3 bucket.
set -e
awslocal s3 mb s3://updatemonitor-dev --region us-east-1
echo "LocalStack: s3://updatemonitor-dev created"
