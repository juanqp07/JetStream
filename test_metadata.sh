#!/bin/bash
echo "--- Testing getArtist.view for external ID ---"
curl -s "http://localhost:8080/rest/getArtist.view?u=admin&p=admin&v=1.16.1&c=test&f=xml&id=ext-squidwtf-artist-606" | xmllint --format -

echo -e "\n--- Testing getAlbum.view for external ID ---"
curl -s "http://localhost:8080/rest/getAlbum.view?u=admin&p=admin&v=1.16.1&c=test&f=xml&id=ext-squidwtf-album-1781800" | xmllint --format -
