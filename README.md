# Hysteria Manager For Keenetic

Лёгкий менеджер `Hysteria2` для Keenetic с импортом подписок из Remnawave.

## Что уже реализовано

- Один встроенный Go-сервис с web UI
- Импорт Remnawave subscription URL с автоматическим `HWID`
- Fallback по `user-agent` (`v2raytun` -> `happ`)
- Извлечение всех `Hysteria2` профилей из Xray-JSON подписки
- Стабильные профили с маскировкой `auth`
- Запуск клиента `hysteria` в TUN mode
- Уникальные интерфейсы вида `opkgtunX` с отдельными TUN-адресами
- Логин в UI с использованием учётных данных Keenetic
- Автообновление подписки
- API для списка туннелей, активации, деактивации, логов и статуса
- Подготовка `OpkgTun`-профилей через `ndmc`, чтобы туннели можно было подхватить в GUI Keenetic

## Важные допущения MVP

- В первой версии менеджер активирует только один туннель одновременно
- `Hysteria` больше не забирает `default route` сама: маршрутизацией должен управлять сам Keenetic
- Для интеграции с NDMS менеджер синхронизирует `OpkgTunN` до запуска активного туннеля
- По умолчанию сервис слушает `0.0.0.0:2230`, чтобы не конфликтовать с `AWG Manager` на `:2222`
- Каталоги состояния:
  - `/opt/etc/hysteria-manager`
  - `/opt/etc/hysteria-manager/profiles`
  - `/opt/var/log/hysteria-manager`

## Локальная сборка

```bash
go build ./...
go test ./...
```

Если `go` не установлен в системе, можно использовать локальный toolchain во временной папке.

## Переменные окружения

- `HM_LISTEN_ADDR` — адрес и порт панели, по умолчанию `0.0.0.0:2230`
- `HM_BASE_DIR` — базовый каталог состояния
- `HM_PROFILES_DIR` — каталог профилей
- `HM_LOG_DIR` — каталог логов
- `HM_STATE_FILE` — путь к `state.json`
- `HM_MANAGER_LOG` — лог менеджера
- `HM_HYSTERIA_LOG` — лог клиента `hysteria`
- `HM_RUNTIME_CONFIG` — runtime YAML для активного туннеля
- `HYSTERIA_BINARY` — путь к бинарнику `hysteria`
- `KEENETIC_BASE_URL` — базовый адрес локального Keenetic API, по умолчанию `http://127.0.0.1`

## Установка через GitHub Releases

```bash
curl -sL https://raw.githubusercontent.com/limeflash/hysteria-keenetic/main/scripts/install.sh | sh
```

Скрипт определяет архитектуру Entware и скачивает соответствующий `.ipk`.
