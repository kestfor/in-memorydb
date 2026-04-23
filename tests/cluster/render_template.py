#!/usr/bin/env python3
"""
render_compose.py — рендерит docker-compose.yaml из Jinja2-шаблона.

Использование:
  python3 render_compose.py --nodes 10
  python3 render_compose.py --nodes 5 --env "WAL_ENABLED=false" "SYNC_INTERVAL=5s"
  python3 render_compose.py --nodes 3 --env "ANTI_ENTROPY_INTERVAL=10s" --output my-compose.yaml
"""

import argparse
import sys
from pathlib import Path

try:
    from jinja2 import Environment, FileSystemLoader
except ImportError:
    print("jinja2 не установлен. Установите: pip install jinja2", file=sys.stderr)
    sys.exit(1)


def parse_env(env_args: list[str]) -> dict[str, str]:
    """Парсит список 'KEY=VALUE' в dict."""
    result = {}
    for item in env_args:
        for pair in item.split(","):
            pair = pair.strip()
            if not pair:
                continue
            if "=" not in pair:
                print(f"Ошибка: некорректный формат '{pair}', ожидается KEY=VALUE", file=sys.stderr)
                sys.exit(1)
            key, value = pair.split("=", 1)
            result[key.strip()] = value.strip()
    return result


def main():
    parser = argparse.ArgumentParser(description="Рендер docker-compose.yaml из Jinja2-шаблона")
    parser.add_argument("-n", "--nodes", type=int, required=True, help="Количество нод (1..N)")
    parser.add_argument(
        "-e", "--env", nargs="*", default=[],
        help='Доп. переменные окружения для всех нод: KEY=VALUE (через пробел или запятую)'
    )
    parser.add_argument(
        "-t", "--template", type=str, default=None,
        help="Путь к шаблону (по умолчанию: docker-compose.yaml.j2 рядом со скриптом)"
    )
    parser.add_argument(
        "-o", "--output", type=str, default="docker-compose.yaml",
        help="Путь для выходного файла (по умолчанию: docker-compose.yaml)"
    )
    args = parser.parse_args()

    if args.nodes < 1:
        print("Ошибка: --nodes должно быть >= 1", file=sys.stderr)
        sys.exit(1)

    extra_env = parse_env(args.env)

    # Определяем путь к шаблону
    if args.template:
        template_path = Path(args.template)
    else:
        template_path = Path(__file__).parent / "docker-compose.yaml.j2"

    if not template_path.exists():
        print(f"Ошибка: шаблон не найден: {template_path}", file=sys.stderr)
        sys.exit(1)

    env = Environment(
        loader=FileSystemLoader(str(template_path.parent)),
        keep_trailing_newline=True,
        trim_blocks=True,
        lstrip_blocks=True,
    )
    template = env.get_template(template_path.name)

    rendered = template.render(nodes=args.nodes, extra_env=extra_env)

    output_path = Path(args.output)
    output_path.write_text(rendered, encoding="utf-8")
    print(f"Сгенерирован {output_path} ({args.nodes} нод)", file=sys.stderr)
    if extra_env:
        print(f"  доп. env: {extra_env}", file=sys.stderr)


if __name__ == "__main__":
    main()