# pushing to unraid
#
# by 3n3a

rsync -avz --exclude='.git*' --exclude='.claude*' --filter='dir-merge,-n /.gitignore' /Volumes/Dev/repos/mediforge/ unraid:/mnt/user/appdata/mediforge/
