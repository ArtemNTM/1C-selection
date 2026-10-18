#!/bin/sh
set -eu

demo_dir=.times-demo
editor_name_file=configuration/editor.name

fail() {
    printf '%s\n' "$1" >&2
    exit 1
}

show_access() {
    printf 'Редактор: %s\n' "$editor_name"
    printf 'При создании редактора сервер выдаст персональный токен и сохранит его в %s/editor.token.\n' "$demo_dir"
    printf 'После запуска сервера показать сохранённый токен: make editor-token\n'
    printf 'Вход после запуска сервера: bin/times --config %s/editor.local.yaml connect\n' "$demo_dir"
}

[ -f "$editor_name_file" ] || fail "Впишите имя первого редактора в $editor_name_file (одна строка)."
editor_name=$(cat "$editor_name_file")
case "$editor_name" in
    *'
'*) fail "В $editor_name_file должно быть одно имя на одной строке." ;;
esac
case "$editor_name" in
    *[![:space:]]*) ;;
    *) fail "Впишите непустое имя первого редактора в $editor_name_file." ;;
esac
if printf '%s' "$editor_name" | LC_ALL=C grep -q '[[:cntrl:]]'; then
    fail 'Имя редактора не должно содержать управляющие символы.'
fi
[ "$(printf '%s' "$editor_name" | wc -m)" -le 100 ] || fail 'Имя редактора должно содержать не больше 100 символов.'
case "$editor_name" in
    tonny|sacharissa) fail 'Имена tonny и sacharissa заняты демонстрационными корреспондентами. Выберите другое имя редактора.' ;;
esac
# YAML single-quoted strings preserve spaces, Cyrillic, colons and quotes in names.
editor_name_yaml=$(printf '%s' "$editor_name" | sed "s/'/''/g")

if [ -e "$demo_dir" ]; then
    for name in server.local.yaml editor.local.yaml tonny.local.yaml sacharissa.local.yaml; do
        if [ ! -s "$demo_dir/$name" ]; then
            fail "Отсутствует или пуст конфиг $demo_dir/$name. Восстановите исходный файл."
        fi
    done
    # Accept both the new quoted format and configs created by the old script.
    if ! grep -Fqx "client_id: '$editor_name_yaml'" "$demo_dir/editor.local.yaml" &&
       ! grep -Fqx "client_id: $editor_name" "$demo_dir/editor.local.yaml"; then
        fail "Доступ редактора уже настроен на другое имя (см. client_id в $demo_dir/editor.local.yaml). Верните его в $editor_name_file; другого редактора создайте через clients create. Изменение файла не переименовывает пользователя в БД."
    fi
    printf 'Используются существующие конфиги из %s.\n' "$demo_dir"
    show_access
    exit 0
fi

umask 077
demo_stage=$(mktemp -d .times-demo.setup.XXXXXX)
trap 'rm -rf "$demo_stage"' 0
trap 'exit 1' HUP INT TERM

for name in editor tonny sacharissa; do
    client_name=$name
    if [ "$name" = editor ]; then
        client_name=$editor_name_yaml
    fi
    cat > "$demo_stage/$name.local.yaml" <<EOF
server: http://127.0.0.1:8080
client_id: '$client_name'
token_file: $demo_dir/$name.token
state_dir: $demo_dir/$name-state
EOF
done

cat > "$demo_stage/server.local.yaml" <<EOF
addr: 127.0.0.1:8080
database_url: postgres://times:times@127.0.0.1:54329/times?sslmode=disable
threshold: 1.1
half_life: 6h
congratulate: 3
cache_refresh: 5m
session_ttl: 24h
clients:
  - name: '$editor_name_yaml'
    kind: staff
    role: editor
    trust: 1
    enabled: true
    token_file: $demo_dir/editor.token
  - name: tonny
    kind: freelance
    role: reporter
    trust: 0.25
    enabled: true
    token_file: $demo_dir/tonny.token
  - name: sacharissa
    kind: staff
    role: reporter
    trust: 1
    enabled: true
    token_file: $demo_dir/sacharissa.token
EOF

mv "$demo_stage" "$demo_dir"
printf 'Конфиги созданы в %s. Пользователей и их токены создаст сервер при запуске.\n' "$demo_dir"
show_access
