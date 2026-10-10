#!/bin/bash

# Batch job runner script
# Usage: ./scripts/batch.sh [job_type]
# Jobs: migrate, seed, rankings, tracked-keywords, all

set -e

JOB_TYPE="${1:-all}"
PROJECT_ID="aso-tool-prod"
REGION="asia-northeast1"

echo "=== ASO Tool Batch Runner ==="
echo "Job: $JOB_TYPE"
echo ""

# Start Cloud SQL if needed
echo "Starting Cloud SQL..."
curl -s "https://db-manager-671942133800.asia-northeast1.run.app?action=start"
echo ""

# Wait for DB to be ready
echo "Waiting for DB to start (30 seconds)..."
sleep 30

# Check DB status
echo "Checking DB status..."
curl -s "https://db-manager-671942133800.asia-northeast1.run.app?action=status"
echo ""
echo ""

# Execute batch job
echo "Executing batch job: $JOB_TYPE"
gcloud run jobs execute aso-batch \
  --region $REGION \
  --args "$JOB_TYPE" \
  --wait

# "all" no longer includes tracked keywords; run them as their own execution
# afterwards, the same way the scheduler does.
if [ "$JOB_TYPE" = "all" ]; then
  echo "Executing batch job: tracked-keywords"
  gcloud run jobs execute aso-batch \
    --region $REGION \
    --args tracked-keywords \
    --wait
fi

echo ""
echo "=== Done ==="
