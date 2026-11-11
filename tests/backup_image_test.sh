#!/bin/sh
# Checks that the production image can back up a MySQL 8+ database: its
# mysqldump signs in to a fresh MySQL (caching_sha2_password, the default
# since MySQL 8) and dumps it, with the options the backup service uses
# (internal/services/backup_service.go, dump). A fresh server and a user that
# has never signed in make it the full sign-in, the case that needs the plugin.
#
#   make test-backup-image                      # builds the image first
#   IMAGE=mart-backend:x sh tests/backup_image_test.sh
#   MYSQL_IMAGE=mysql:9.1 make test-backup-image  # another MySQL
#
# Everything it starts is removed at the end, pass or fail.
set -eu

IMAGE="${IMAGE:-mart-backend:backup-check}"
MYSQL_IMAGE="${MYSQL_IMAGE:-mysql:8.4}"
NAME="mart-backup-check-$$"
PASSWORD="backup-check-only-password"

cleanup() {
	docker rm -f "$NAME-db" >/dev/null 2>&1 || true
	docker network rm "$NAME" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

docker network create "$NAME" >/dev/null
docker run -d --name "$NAME-db" --network "$NAME" --network-alias db --tmpfs /var/lib/mysql \
	-e MYSQL_DATABASE=shop -e MYSQL_USER=shop -e MYSQL_PASSWORD="$PASSWORD" -e MYSQL_ROOT_PASSWORD="$PASSWORD" \
	"$MYSQL_IMAGE" >/dev/null

# Ready once root can sign in over TCP; the shop user is left untouched.
tries=0
until docker exec -e MYSQL_PWD="$PASSWORD" "$NAME-db" mysql -h127.0.0.1 -uroot -e 'SELECT 1' >/dev/null 2>&1; do
	tries=$((tries + 1))
	if [ "$tries" -gt 120 ]; then
		echo "FAIL: $MYSQL_IMAGE didn't start" >&2
		exit 1
	fi
	sleep 1
done
docker exec -e MYSQL_PWD="$PASSWORD" "$NAME-db" mysql -h127.0.0.1 -uroot shop \
	-e "CREATE TABLE backup_check (id INT PRIMARY KEY); INSERT INTO backup_check VALUES (42);"
plugin=$(docker exec -e MYSQL_PWD="$PASSWORD" "$NAME-db" mysql -h127.0.0.1 -uroot -N \
	-e "SELECT plugin FROM mysql.user WHERE user = 'shop'")
version=$(docker exec -e MYSQL_PWD="$PASSWORD" "$NAME-db" mysql -h127.0.0.1 -uroot -N -e "SELECT VERSION()")

# The same options as the backup service, run as the image runs the app.
dump() {
	docker run --rm --network "$NAME" -e MYSQL_PWD="$PASSWORD" --entrypoint mysqldump "$IMAGE" \
		--single-transaction --quick --routines --triggers --no-tablespaces \
		--default-character-set=utf8mb4 --protocol=TCP -h db -P 3306 -u shop shop
}

for attempt in "first sign-in" "signed in before"; do
	if ! out=$(dump 2>&1); then
		echo "FAIL ($attempt): $IMAGE can't back up MySQL $version (user plugin $plugin):" >&2
		echo "$out" >&2
		exit 1
	fi
	# MariaDB's mysqldump puts the rows on the line after VALUES.
	case "$out" in
	*"INSERT INTO \`backup_check\` VALUES"*"(42);"*) ;;
	*)
		echo "FAIL ($attempt): the dump is missing the test row:" >&2
		echo "$out" | head -20 >&2
		exit 1
		;;
	esac
done
echo "ok: $IMAGE backs up MySQL $version (user plugin $plugin)"
