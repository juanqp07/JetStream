#!/bin/bash

# Base URL (Adjust if running in different environment)
URL="http://localhost:8080/rest"

echo "Testing getOpenSubsonicExtensions (XML)..."
curl -s "$URL/getOpenSubsonicExtensions.view?u=test&p=test&v=1.16.1&c=test" | grep -q "openSubsonicExtensions" && echo "PASS" || echo "FAIL"

echo "Testing getOpenSubsonicExtensions (JSON)..."
curl -s "$URL/getOpenSubsonicExtensions.view?u=test&p=test&v=1.16.1&c=test&f=json" | grep -q "\"openSubsonicExtensions\"" && echo "PASS" || echo "FAIL"

echo "Testing search3 (JSON)..."
curl -s "$URL/search3.view?u=test&p=test&v=1.16.1&c=test&query=love&f=json" | grep -q "\"searchResult3\"" && echo "PASS" || echo "FAIL"

echo "Testing getAlbum (XML - External ID)..."
# Using a dummy external ID that might fail Squid but should return a Subsonic Error XML
curl -s "$URL/getAlbum.view?u=test&p=test&v=1.16.1&c=test&id=ext-squidwtf-album-123" | grep -q "subsonic-response" && echo "PASS" || echo "FAIL"
