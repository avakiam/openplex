if [ -f index.html ] && grep -q "This site is hosted with OpenPlex" index.html; then
	rm -f index.html
fi
if [ -n "$(ls -A)" ]; then
	echo "the document root is not empty" >&2
	exit 3
fi
