# Fall back to the community-pool preset if the flight sheet left the pool URL blank.
: "${CUSTOM_URL:=community-pools.mysrv.cloud:10300}"
if [[ $CUSTOM_URL == wss* ]]; then
    USERNAME=`echo $CUSTOM_TEMPLATE | cut -d . -f 1`
else
    USERNAME=$CUSTOM_TEMPLATE
fi
# Fall back to the default wallet if the flight sheet left the wallet blank.
: "${USERNAME:=dero1qyvuemd6z0uzsx5ufc99f0jhyzvvpysmrd2t3526ht7a9dfh7jve2qqt0vu5y}"
echo -e "-d $CUSTOM_URL -w $USERNAME $CUSTOM_USER_CONFIG" > $CUSTOM_CONFIG_FILENAME
