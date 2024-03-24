FROM mcr.microsoft.com/devcontainers/base:bullseye
RUN sudo apt update
RUN sudo apt install -y linux-perf
# needs to match uname -r, which comes from the container host, not the image
RUN sudo ln -s /usr/bin/perf_5.10 /usr/bin/perf_6.6