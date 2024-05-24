sudo apt update
sudo apt install man-db manpages-posix manpages-dev manpages-posix-dev vim less
pip install ipython matplotlib numpy pandas ipympl duckdb
[ -f .devcontainer/personalPostCreate.sh ] && bash .devcontainer/personalPostCreate.sh