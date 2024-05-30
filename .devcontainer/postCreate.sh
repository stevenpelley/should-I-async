sudo apt update
sudo apt install -y man-db manpages-posix manpages-dev manpages-posix-dev vim less
pip install ipython matplotlib numpy pandas ipympl duckdb ipykernel
[ -f .devcontainer/personalPostCreate.sh ] && bash .devcontainer/personalPostCreate.sh