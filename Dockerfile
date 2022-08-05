FROM python
RUN  useradd -ms /bin/bash gget && pip3 install --upgrade pip && pip3 install git-dumper
USER gget