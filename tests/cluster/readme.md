# 10 нод, без доп. переменных
python3 render_compose.py --nodes 10

# 5 нод с общими env для всех
python3 render_compose.py --nodes 5 --env "WAL_ENABLED=false" "ANTI_ENTROPY_INTERVAL=10s"

# Кастомный выход
python3 render_compose.py --nodes 3 -o my-compose.yaml