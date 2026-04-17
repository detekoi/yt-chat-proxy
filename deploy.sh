#!/bin/bash
set -e

PROJECT_ID="chat-themer"
SERVICE_NAME="yt-chat-proxy"
IMAGE_TAG="us-central1-docker.pkg.dev/$PROJECT_ID/chat-theme-repo/$SERVICE_NAME:latest"
REGION="us-central1"
GCLOUD_BIN="$HOME/google-cloud-sdk/bin/gcloud"

echo "Setting GCP project to $PROJECT_ID..."
$GCLOUD_BIN config set project $PROJECT_ID

echo "Building container image..."
$GCLOUD_BIN builds submit --project=$PROJECT_ID --tag $IMAGE_TAG

echo "Deploying to Cloud Run in $REGION..."
$GCLOUD_BIN run deploy $SERVICE_NAME \
  --project=$PROJECT_ID \
  --image $IMAGE_TAG \
  --region $REGION \
  --platform managed \
  --allow-unauthenticated

echo "Deployment completed successfully!"
