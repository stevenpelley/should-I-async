FROM ubuntu:24.04

# use the default ubuntu user but make sure we can sudo without password
ARG USERNAME=ubuntu

RUN yes | unminimize
RUN apt-get update
RUN apt-get install -y locales-all
RUN echo "LC_ALL=en_US.UTF-8" > /etc/default/locale
RUN echo "LANG=en_US.UTF-8" >> /etc/default/locale
RUN apt-get install -y sudo
RUN echo $USERNAME ALL=\(root\) NOPASSWD:ALL > /etc/sudoers.d/$USERNAME
RUN chmod 0440 /etc/sudoers.d/$USERNAME
USER $USERNAME
WORKDIR /home/$USERNAME

