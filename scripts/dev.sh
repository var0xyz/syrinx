#!/usr/bin/env bash

SESSION="syrinx"

# Ensure dockerd is running
if ! docker info > /dev/null 2>&1; then
  if [ "$(uname -s)" = "Linux" ]; then
    echo "Starting Docker..."
    sudo systemctl start docker.socket docker.service
  elif [ "$(uname -s)" = "Darwin" ]; then
    echo "Starting Docker Desktop..."
    open -a Docker
    echo "Waiting for Docker daemon..."
    until docker info > /dev/null 2>&1; do sleep 1; done
  else
    echo "Error: Docker is not running and unsupported OS '$(uname -s)'."
    exit 1
  fi
fi

# Check that required ports are free
for port in 5173 8000 8080; do
  if lsof -i TCP:"${port}" -sTCP:LISTEN -t > /dev/null 2>&1; then
    echo "Error: port ${port} is already in use."
    exit 1
  fi
done

tmux new-session -d -s "$SESSION" -n "database"
tmux send-keys -t "$SESSION:database" "cd ~/code/syrinx; docker-compose up db" Enter

tmux split-window -t "$SESSION:database" -v
tmux send-keys -t "$SESSION:database.1" "sleep 3 && docker exec -it syrinx_db psql -U syrinx -d syrinx" Enter

tmux new-window -t "$SESSION" -n "api"
tmux send-keys -t "$SESSION:api" "sleep 3; cd ~/code/syrinx; source .env; make run" Enter

tmux new-window -t "$SESSION" -n "spa"
tmux send-keys -t "$SESSION:spa" "cd ~/code/syrinx/spa; npm run dev -- --host" Enter

tmux split-window -t "$SESSION:spa" -v
tmux send-keys -t "$SESSION:spa.1" "cd ~/Pictures; python -m http.server" Enter

tmux new-window -t "$SESSION" -n "claude"
tmux send-keys -t "$SESSION:claude" "cd ~/code/syrinx; claude" Enter

tmux new-window -t "$SESSION" -n "code"
tmux send-keys -t "$SESSION:code" "cd ~/code/syrinx" Enter

tmux attach-session -t "$SESSION"
