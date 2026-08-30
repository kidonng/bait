function __bait_ostype --no-scope-shadowing
    if set --query __bait_ostype_val
        echo $__bait_ostype_val
        return 0
    end
    set --local os (uname -s | string lower)
    switch $os
        case linux
            if test -n "$ANDROID_ROOT"
                set --global __bait_ostype_val linux-android
            else if test -f /etc/alpine-release
                set --global __bait_ostype_val linux-musl
            else
                set --global __bait_ostype_val linux-gnu
            end
        case darwin
            set --local rel (uname -r | string replace --regex '\..*' '')
            set --global __bait_ostype_val "darwin$rel"
        case freebsd
            set --local rel (uname -r | string lower)
            set --global __bait_ostype_val "freebsd$rel"
        case openbsd
            set --local rel (uname -r | string lower)
            set --global __bait_ostype_val "openbsd$rel"
        case netbsd
            set --global __bait_ostype_val netbsd
        case dragonfly
            set --global __bait_ostype_val dragonfly
        case solaris
            set --global __bait_ostype_val solaris
        case '*'
            set --global __bait_ostype_val $os
    end
    echo $__bait_ostype_val
end
