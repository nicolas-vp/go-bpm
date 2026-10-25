#!/bin/sh
# Выкладка демо go-bpm на сервер из CI (job deploy в .github/workflows/ci.yml).
#
# Ключ CI прописан в authorized_keys с command="/opt/go-bpm/deploy.sh" и restrict: этот ключ может только
# вызвать скрипт. Аргументы - в SSH_ORIGINAL_COMMAND: "<тег образа> [<пользователь ghcr> <токен>]",
# на stdin - compose.yaml из коммита. Токен (GITHUB_TOKEN джоба) нужен, пока пакет в ghcr.io приватный;
# он живёт только во временном DOCKER_CONFIG и истекает вместе с джобом.
#
# В каталоге скрипта лежат compose.yaml и .env (POSTGRES_PASSWORD, GO_BPM_TAG, GO_BPM_LISTEN).
# Контейнер приложения пересоздаётся из нового образа, старые образы go-bpm удаляются, база и её том остаются.
set -eu
set -f

dir=$(dirname "$0")
cd "$dir"

set -- ${SSH_ORIGINAL_COMMAND:-}
if [ $# -lt 1 ]; then
	echo "использование: <тег образа> [<пользователь ghcr> <токен>]" >&2
	exit 2
fi
tag=$1
case $tag in
'' | *[!A-Za-z0-9._-]*)
	echo "недопустимый тег образа: $tag" >&2
	exit 2
	;;
esac

# compose.yaml из коммита: заменяет текущий, только если docker compose его принимает
cat >compose.yaml.new
if ! docker compose -f compose.yaml.new --env-file .env config -q; then
	rm -f compose.yaml.new
	echo "compose.yaml из коммита не принят" >&2
	exit 1
fi
mv compose.yaml.new compose.yaml

DOCKER_CONFIG=$(mktemp -d)
export DOCKER_CONFIG
trap 'rm -rf "$DOCKER_CONFIG"' EXIT
if [ $# -ge 3 ]; then
	printf '%s' "$3" | docker login ghcr.io -u "$2" --password-stdin
fi

GO_BPM_TAG=$tag docker compose pull go-bpm

# тег запоминается в .env, когда образ уже скачан: ручной docker compose up поднимет ту же версию
if grep -q '^GO_BPM_TAG=' .env; then
	sed -i "s/^GO_BPM_TAG=.*/GO_BPM_TAG=$tag/" .env
else
	echo "GO_BPM_TAG=$tag" >>.env
fi

docker compose up -d --no-build --remove-orphans --wait --wait-timeout 120
# старые сборки: все прочие теги go-bpm и оставшиеся без тега слои
docker images --format '{{.Repository}}:{{.Tag}}' ghcr.io/nicolas-vp/go-bpm |
	grep -vx "ghcr.io/nicolas-vp/go-bpm:$tag" | xargs -r docker rmi || true
docker image prune -f

# сервис отвечает: миграции применились, HTTP поднялся
listen=$(sed -n 's/^GO_BPM_LISTEN=//p' .env)
listen=${listen:-127.0.0.1:8080}
case $listen in 0.0.0.0:*) listen=127.0.0.1:${listen#0.0.0.0:} ;; esac
i=0
until curl -fsS -o /dev/null "http://$listen/ui/"; do
	i=$((i + 1))
	if [ $i -ge 30 ]; then
		echo "go-bpm не отвечает на http://$listen/ui/" >&2
		docker compose logs --tail 50 go-bpm >&2
		exit 1
	fi
	sleep 2
done
docker compose ps
echo "выложен ghcr.io/nicolas-vp/go-bpm:$tag"
