function unalias --no-scope-shadowing
	for arg in $argv
		if test -n "$arg" -a "$arg" != "-a"
			functions --erase -- $arg 2>/dev/null
		end
	end
	return 0
end
