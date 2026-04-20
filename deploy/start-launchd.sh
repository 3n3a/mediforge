# start launchd
#
# by 3n3a

launchctl unload ~/Library/LaunchAgents/tech.enea.mediforge-worker.plist
launchctl load ~/Library/LaunchAgents/tech.enea.mediforge-worker.plist
launchctl start tech.enea.mediforge-worker
